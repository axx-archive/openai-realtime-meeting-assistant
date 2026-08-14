# STRIDE Project/workstream retirement rendered checkpoint

This directory is bounded local evidence for the founder decision that Project
association belongs to STRIDE's current authorized workstream understanding,
not to a chat-composer control or a Kanban Board.

The synthetic browser journey proves:

- the chat composer has no visible `Add project` control;
- one delivered Research Work record retains its current Project attribution;
- desktop's Work inspector and the compact phone Work card expose the same
  result-side `Change project` action;
- the correction dialog states that the source conversation remains unchanged;
- the client submits only an opaque correction token and stable operation ID;
- the accepted correction reprojects the same Work from `Research Project` to
  `Strategy Project` without creating another work card; and
- the existing Open, Save to Drive and Regenerate actions remain usable.

## Reproduction

From `/Users/ajhart/meetingassist`:

```sh
PROJECT_WORK_RENDER_DIR=docs/evidence/e10/rendered-project-workstream-retirement-20260814 \
  go test . -run '^TestProjectBoundResearchRenderedOpenDriveAndRegenerateJourney$' -count=1
```

The fixture uses only synthetic identities, artifacts and Project names. It
does not contact production, Apple, a model provider or Drive.

`CANDIDATE-MANIFEST.txt` binds the reviewed source/test scope, and
`SHA256SUMS` binds the retained PNGs. This is local dirty-tree evidence only:
it is not a commit, signed iOS carrier, deployment, TestFlight observation or
physical-device acceptance result.
