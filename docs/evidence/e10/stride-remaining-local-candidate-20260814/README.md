# STRIDE remaining local candidate receipt

This directory freezes the exact local implementation candidate that remained
after the 2026-08-14 STRIDE rebaseline and safe implementation waves.

`CANDIDATE-MANIFEST.txt` is body-free. It binds the baseline commit, branch,
tracked state, file mode, byte count, and SHA-256 digest for each of the 43
implementation files. It intentionally excludes:

- `docs/plans/stride-next-evolution-master-plan.md`, because that is the live
  execution ledger and will point back to this receipt;
- this evidence directory, to avoid a self-referential digest;
- the unrelated `stride-site/` tree;
- ignored runtime or test artifacts.

The receipt grants no authority to stage, commit, push, deploy, call paid
providers, change Apple state, install a migration, repair production data, or
otherwise mutate external state.

## Verification

From the repository root, require the current branch and baseline commit to
match the manifest. For every tab-separated entry after `columns=`, require
the current file state, mode, byte count, and SHA-256 to match exactly. Also
require the current implementation candidate path set, after applying the four
documented exclusions, to contain exactly 43 paths and no additions.

The final local validation bound to these bytes was:

- full main Go package: PASS (`614.299s`);
- Go vet: PASS;
- focused normal and race suites: PASS;
- native suite: PASS (`559/559`) with typecheck PASS;
- room incident evaluator: PASS (`7/7`);
- formatting and whitespace checks: PASS;
- independent critic gates: PASS with no current P0-P2 findings.

These are local candidate results, not release, live-provider, migration,
production-repair, Apple-device, or two-person incident-soak acceptance.
