#!/usr/bin/env bash
# Installs Multus (thick plugin), Whereabouts, and the macvlan/ipvlan/static
# CNI plugin binaries into the kind cluster. Versions pinned in dev-docs/versions.md.
set -euo pipefail

MULTUS_TAG=v4.3.1
WHEREABOUTS_TAG=v0.9.4
CNI_PLUGINS_TAG=v1.9.1

# kind's node image ships only the handful of CNI binaries kindnet itself
# needs (host-local, loopback, portmap, ptp) - macvlan, ipvlan, and the
# static IPAM plugin (used by voice-static) aren't there, so every pod on
# the voice NADs fails with "failed to find plugin ... in path
# [/opt/cni/bin]" until these are copied in.
install_cni_plugins() {
  local edge_nodes tmp
  edge_nodes=$(kubectl get nodes -l voice-role=voice -o jsonpath='{.items[*].metadata.name}')
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  curl -sSL -o "$tmp/cni-plugins.tgz" \
    "https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_TAG}/cni-plugins-linux-arm64-${CNI_PLUGINS_TAG}.tgz"
  curl -sSL -o "$tmp/cni-plugins.tgz.sha256" \
    "https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_TAG}/cni-plugins-linux-arm64-${CNI_PLUGINS_TAG}.tgz.sha256"
  (cd "$tmp" && sha256sum -c <(sed 's/cni-plugins-linux-arm64-.*\.tgz/cni-plugins.tgz/' cni-plugins.tgz.sha256) \
    || shasum -a 256 -c <(sed 's/cni-plugins-linux-arm64-.*\.tgz/cni-plugins.tgz/' cni-plugins.tgz.sha256))
  tar xzf "$tmp/cni-plugins.tgz" -C "$tmp" macvlan ipvlan static

  for node in $edge_nodes; do
    if podman exec "$node" test -x /opt/cni/bin/macvlan 2>/dev/null; then
      continue
    fi
    for plugin in macvlan ipvlan static; do
      podman cp "$tmp/$plugin" "$node:/opt/cni/bin/$plugin"
      podman exec "$node" chmod +x "/opt/cni/bin/$plugin"
    done
  done
}

install_cni_plugins

kubectl apply -f "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/${MULTUS_TAG}/deployments/multus-daemonset-thick.yml"
kubectl -n kube-system rollout status daemonset/kube-multus-ds --timeout=120s

for f in daemonset-install.yaml \
         whereabouts.cni.cncf.io_ippools.yaml \
         whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml \
         reconciler-deployment.yaml; do
  kubectl apply -f "https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/${WHEREABOUTS_TAG}/doc/crds/${f}"
done
kubectl -n kube-system rollout status daemonset/whereabouts --timeout=120s

echo "Multus ${MULTUS_TAG} and Whereabouts ${WHEREABOUTS_TAG} ready."
