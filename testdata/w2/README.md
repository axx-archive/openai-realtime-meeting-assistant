# W2 live-evidence custody

The W2 gate manifest pins three pieces of the evidence path to the release
checkout:

- the checked collector/client implementation files;
- an independent Ed25519 collector public key (the private key is never stored
  in this repository or passed to `w2gate`);
- a separately signed, pinned USD price catalog.

Run the collector from the exact release commit with externally custodied
secrets and body-free evidence bundles:

```sh
go run ./cmd/w2-collector \
  --manifest testdata/w2/gates.json \
  --release-commit "$BONFIRE_RELEASE_COMMIT" \
  --bundle-dir /run/bonfire-w2/evidence \
  --private-key-file /run/secrets/bonfire-w2-collector-ed25519.json \
  --token-file /run/secrets/bonfire-w2-collector-token \
  --listen 127.0.0.1:8099
```

Gate bundles are named `gate-<gate-id>.json` and use
`bonfire.w2.collector.gate-bundle.v1`. Probe bundles are named
`probe-<probe-id-with-colons-replaced-by-dashes>.json` and use
`bonfire.w2.collector.probe-bundle.v1`.

Bundles contain no transcript, prompt, response, reference, or judge bodies.
They contain the frozen corpus item digest, provider request ID and
request/response digests, reference-label and judge-artifact digests, typed
per-item primitives, and usage counts. The checked client rejects incomplete
corpus coverage and recomputes every metric and estimated cost locally.

The collector binds each signed response to a cryptographically random,
one-use challenge and the pinned collector implementation digest. Missing
bundles, pending corpora, invalid provider evidence, absent secrets, or an
unavailable collector all fail closed and cannot produce a W2 receipt.
