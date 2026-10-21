#!/usr/bin/env bash
# Renders the platform chart for a cluster, then every Application it produces (upstream
# charts with their computed values, and the config charts), and validates all manifests
# with kubeconform. Used locally and in CI.
#
# Usage: scripts/check-gitops.sh [clusters/<name>/platform-values.yaml]
set -euo pipefail

cd "$(dirname "$0")/.."
values=${1:-gitops/clusters/example/values.yaml}
# Charts check this against their kubeVersion constraints; keep it in line with k3s_version.
kube_version=${KUBE_VERSION:-1.36.5}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

schemas=(-schema-location default
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json')

helm template root gitops/platform -f "$values" > "$work/applications.yaml"
kubeconform -strict -summary "${schemas[@]}" "$work/applications.yaml"

# One JSON line per Application: name, namespace and source.
ruby -ryaml -rjson -e '
  YAML.load_stream(File.read(ARGV[0])).compact.each do |app|
    puts({ name: app["metadata"]["name"], namespace: app["spec"]["destination"]["namespace"],
           source: app["spec"]["source"] }.to_json)
  end' "$work/applications.yaml" > "$work/apps.jsonl"

failed=0
while IFS= read -r app; do
  name=$(jq -r .name <<<"$app")
  ns=$(jq -r '.namespace // "default"' <<<"$app")
  jq '.source.helm.valuesObject // {}' <<<"$app" > "$work/$name.values.json"
  if jq -e '.source.chart' <<<"$app" >/dev/null; then
    src=(--repo "$(jq -r .source.repoURL <<<"$app")" "$(jq -r .source.chart <<<"$app")"
         --version "$(jq -r .source.targetRevision <<<"$app")")
  elif jq -e '.source.path | startswith("gitops/config/")' <<<"$app" >/dev/null; then
    src=("$(jq -r .source.path <<<"$app")")
  else
    echo "skip  $name (plain Git directory)"; continue
  fi
  if helm template "$name" "${src[@]}" -n "$ns" -f "$work/$name.values.json" \
       --kube-version "$kube_version" --api-versions monitoring.coreos.com/v1 > "$work/$name.yaml" 2> "$work/$name.err" &&
     kubeconform -strict -ignore-missing-schemas "${schemas[@]}" "$work/$name.yaml" > "$work/$name.kc" 2>&1; then
    echo "ok    $name"
  else
    echo "FAIL  $name"; cat "$work/$name.err" "$work/$name.kc" 2>/dev/null | head -20; failed=1
  fi
done < "$work/apps.jsonl"

exit $failed
