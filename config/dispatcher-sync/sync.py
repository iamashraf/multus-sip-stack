#!/usr/bin/env python3
"""Keeps Kamailio dispatcher sets in sync with the FreeSWITCH EndpointSlice.

Every tick: read the ready pod IPs from the FreeSWITCH EndpointSlice, then
reconcile each Kamailio instance's *live* dispatcher.list against that set
(not against what we last pushed) - a restarted Kamailio reboots from its
static ConfigMap and needs to be corrected the same way a scale event does.
"""
import json
import os
import ssl
import time
import urllib.error
import urllib.request

NAMESPACE = os.environ["NAMESPACE"]
FREESWITCH_SERVICE = os.environ["FREESWITCH_SERVICE"]
KAMAILIO_TARGETS = os.environ["KAMAILIO_TARGETS"].split(",")
POLL_INTERVAL_SECONDS = float(os.environ.get("POLL_INTERVAL_SECONDS", "3"))
DISPATCHER_GROUP = int(os.environ.get("DISPATCHER_GROUP", "1"))
PORT = 5060

SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount"
with open(f"{SA_DIR}/token") as f:
    TOKEN = f.read().strip()
API_HOST = os.environ["KUBERNETES_SERVICE_HOST"]
API_PORT = os.environ["KUBERNETES_SERVICE_PORT"]
SSL_CTX = ssl.create_default_context(cafile=f"{SA_DIR}/ca.crt")


def k8s_get(path):
    req = urllib.request.Request(
        f"https://{API_HOST}:{API_PORT}{path}",
        headers={"Authorization": f"Bearer {TOKEN}"},
    )
    with urllib.request.urlopen(req, context=SSL_CTX, timeout=10) as r:
        return json.load(r)


def ready_freeswitch_ips():
    data = k8s_get(
        f"/apis/discovery.k8s.io/v1/namespaces/{NAMESPACE}/endpointslices"
        f"?labelSelector=kubernetes.io/service-name={FREESWITCH_SERVICE}"
    )
    ips = set()
    for item in data.get("items", []):
        for ep in item.get("endpoints", []):
            if ep.get("conditions", {}).get("ready", True):
                ips.update(ep.get("addresses", []))
    return ips


def rpc(target, method, params):
    body = json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": 1}).encode()
    req = urllib.request.Request(
        f"http://{target}.{NAMESPACE}.svc.cluster.local:8080/RPC",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.load(r)


def current_dispatcher_ips(target):
    result = rpc(target, "dispatcher.list", [])
    ips = set()
    for record in result.get("result", {}).get("RECORDS", []):
        if record["SET"]["ID"] != DISPATCHER_GROUP:
            continue
        for t in record["SET"]["TARGETS"]:
            # URI looks like "sip:10.244.3.35:5060"
            ips.add(t["DEST"]["URI"].split(":")[1])
    return ips


def diff(desired, current):
    return desired - current, current - desired


# Self-check for the reconcile logic above - cheap, runs on every start.
assert diff({1, 2, 3}, {2, 3}) == ({1}, set())
assert diff({2, 3}, {1, 2, 3}) == (set(), {1})
assert diff(set(), set()) == (set(), set())


def sync_target(target, desired_ips):
    try:
        current_ips = current_dispatcher_ips(target)
    except (urllib.error.URLError, OSError, KeyError, ValueError) as exc:
        print(f"{target}: skipping, dispatcher.list failed: {exc}", flush=True)
        return
    to_add, to_remove = diff(desired_ips, current_ips)
    for ip in to_add:
        try:
            rpc(target, "dispatcher.add", [DISPATCHER_GROUP, f"sip:{ip}:{PORT}", 0, 0])
            print(f"{target}: added {ip}", flush=True)
        except (urllib.error.URLError, OSError) as exc:
            # A transient 500 ("Ongoing reload") here just means we retry
            # next tick - it must not take down the whole sync loop.
            print(f"{target}: add {ip} failed, will retry next tick: {exc}", flush=True)
    for ip in to_remove:
        try:
            rpc(target, "dispatcher.remove", [DISPATCHER_GROUP, f"sip:{ip}:{PORT}"])
            print(f"{target}: removed {ip}", flush=True)
        except (urllib.error.URLError, OSError) as exc:
            print(f"{target}: remove {ip} failed, will retry next tick: {exc}", flush=True)


def main():
    while True:
        try:
            desired = ready_freeswitch_ips()
        except (urllib.error.URLError, OSError, ValueError) as exc:
            # Never treat a failed lookup as "no endpoints" - that would
            # wipe both dispatchers on a single API blip.
            print(f"endpointslice lookup failed, skipping tick: {exc}", flush=True)
            time.sleep(POLL_INTERVAL_SECONDS)
            continue
        for target in KAMAILIO_TARGETS:
            sync_target(target, desired)
        time.sleep(POLL_INTERVAL_SECONDS)


if __name__ == "__main__":
    main()
