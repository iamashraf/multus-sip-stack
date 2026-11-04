#!/bin/sh
# preStop hook: mark this pod inactive in both Kamailio dispatchers (no
# new calls arrive), then wait for whatever calls it already has to
# finish before letting Kubernetes kill it. terminationGracePeriodSeconds
# must be longer than DRAIN_TIMEOUT or Kubernetes kills the pod anyway.
set -eu

POD_IP=$(hostname -i | awk '{print $1}')
URI="sip:${POD_IP}:5060"
NS=${FS_NAMESPACE:-voice}

for kam in kamailio-a kamailio-b; do
  wget -q -O /dev/null -T 3 \
    --header="Content-Type: application/json" \
    --post-data="{\"jsonrpc\":\"2.0\",\"method\":\"dispatcher.set_state\",\"params\":[\"i\",1,\"$URI\"],\"id\":1}" \
    "http://$kam.$NS.svc.cluster.local:8080/RPC" 2>/dev/null || true
done

deadline=$(( $(date +%s) + ${DRAIN_TIMEOUT:-50} ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  count=$(fs_cli -H 127.0.0.1 -P 8021 -p ClueCon -x "show calls count" 2>/dev/null | grep -o '[0-9]*' | head -1)
  [ "${count:-1}" = "0" ] && break
  sleep 2
done
