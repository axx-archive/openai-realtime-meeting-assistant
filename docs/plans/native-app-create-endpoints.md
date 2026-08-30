# Create-new for Deck Studio and Document Studio

Status: proposed. Written 2026-08-30 alongside the Installed apps launcher
(`d0105386`), which ships **New** wired to the Scout brief because the two
studios cannot currently create anything.

## Why this is needed

The Work tab now lists both native studios, but neither can start an empty
document:

- `/artifacts/document` (`documentEditorHandler`) accepts **GET and PATCH only**.
- `/artifacts/deck` is the same shape.
- `/artifacts/document/copies` and `/artifacts/deck/copies` both **require a
  source `artifactId`** and authorize with
  `authorizedArtifactForActions(..., ACLReadContent, ACLWrite, ACLCreateChild)`
  against that prior artifact.
- `openDeckEditor` is preview-only — its `saveDeck` toasts *"Preview updated for
  this session — it was not saved."* The persisted path is `openDeckStudio`,
  which likewise needs an existing artifact id.

So "New presentation" / "New document" cannot be built client-side at all. It is
a backend gap, not a UI gap.

## Shape

Two new endpoints, modelled directly on the `*_copies` handlers minus the source
artifact. They are separate surfaces rather than a `POST` on the existing paths
because creation needs `ACLCreateChild` on the destination folder, which the
current `http.artifacts.document` / `http.artifacts.deck` surfaces do not carry.

```
POST /artifacts/document/new
POST /artifacts/deck/new
```

Request (both):

```json
{ "title": "Untitled document", "fileName": "untitled.md", "folderId": "" }
```

Response: the same envelope the editors already consume on open, so the client
can hand the result straight to `openDocumentStudio` / `openDeckStudio`:

```json
{ "ok": true, "artifact": { "id": "...", "version": 1, ... }, "document": { "markdown": "" } }
```

## Handler outline (document; deck is the same with deck payload/asset shape)

Follow `documentEditorCopyHandler` verbatim for the preamble — it already encodes
every guard this needs:

1. `r.Method != http.MethodPost` → 405.
2. `websocketOriginAllowed(r)` → 403.
3. `userFromRequest(r)`, `kanbanApp`, `kanbanApp.memory` → 401 / 503.
4. The `strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive)`
   re-entry hook, byte-for-byte as in the copy handler — creation lands a Drive
   file, so it is the same tenant surface.
5. Decode under `http.MaxBytesReader(w, r.Body, documentStudioMaxBytes+64<<10)`.
6. Validate: non-empty `title` of `<= 160` runes, `normalizeAssistantFileName`
   on `fileName`, and `validateDocumentStudioDocument` on the empty document.
7. `fileFolderWritableFromContext(r.Context(), user, payload.FolderID)` → 404.
   **This replaces the source-artifact authorization** and is the only ACL check
   a blank create needs.

Then create the entry with the copy handler's metadata map, dropping every
`copiedFrom*` key and the asset carry-over:

```go
storedBody, emptyMarker := documentStudioStoredBody("")
metadata := map[string]string{
    "title": payload.Title, "type": artifactTypeMarkdown, "source": "native_studio",
    "status": artifactStatusComplete, "threadStatus": artifactStatusComplete,
    "documentSchemaVersion": "1",
    documentStudioEmptyMetadataKey: emptyMarker,
    "visibility": "organization",
    "ownerEmail": normalizeAccountEmail(user.Email),
    "tenantId":   strideE10TenantIDFromContext(r.Context()),
}
```

`source: "native_studio"` is deliberately a new value — it is what lets Work and
Drive tell a hand-started file from a Scout deliverable, and it keeps
`studioProjectDisplayTitle` from ever seeing a prompt for these.

No `withAuthoredCopySourceOperation` wrapper: there is no source revision to
serialize against, so the compare-and-swap that guard exists for does not apply.

## Authorization surfaces (required — every route is registered)

Add to `authorization_surfaces.go` beside the existing copy entries:

```go
authSurface("http.artifacts.document_new", AuthorizationHTTP, "/artifacts/document/new",
    []string{"artifact", "revision", "file", "folder"},
    []ACLAction{ACLWrite, ACLCreateChild}, []string{"user"}, true, false,
    AuthorizationCanonicalNeeded),
authSurface("http.artifacts.deck_new", AuthorizationHTTP, "/artifacts/deck/new",
    []string{"artifact", "revision", "blob", "file", "folder"},
    []ACLAction{ACLWrite, ACLCreateChild}, []string{"user"}, true, true,
    AuthorizationCanonicalEnforced),
```

Note the asymmetry is inherited, not invented: the document surfaces are
`AuthorizationCanonicalNeeded` and the deck surfaces are
`...Enforced` with `blob` in their object list. Match the sibling you are
extending rather than normalizing the two.

`ACLReadContent` is deliberately absent — nothing is read.

## Client wiring

In the Installed apps launcher (`[data-studio-app-action="new"]`), replace the
`startStudioProjectWithScout()` call for each kind:

```js
const res = await fetch(`/artifacts/${kind === 'document' ? 'document' : 'deck'}/new`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ title: 'Untitled', fileName: 'untitled', folderId: '' })
})
const payload = await res.json()
if (payload?.artifact?.id) {
  kind === 'document'
    ? await openDocumentStudio(payload.artifact.id, payload.artifact.title, {})
    : await openDeckStudio(payload.artifact.id, payload.artifact.title, {})
}
```

Keep the Scout brief reachable — it is the better path for "make me a deck about
X". New should mean *empty editor*, not *replace the brief*.

## Tests to add

- Method guard, cross-origin rejection, unauthenticated 401.
- Unwritable/unknown `folderId` → 404 (the ACL check that replaces the source
  authorization — the one genuinely new authorization path here).
- Created artifact opens through `documentEditorHandler` GET at version 1 with
  empty markdown and `documentStudioEmptyMetadataKey` set.
- The new surfaces appear in the authorization-surface inventory test that
  already asserts every route is registered.
- Frontend: the launcher's New button calls the endpoint and opens the studio
  (mirror `frontend_work_studios_test.go`).

## Deliberately out of scope

Templates, a "new from this deck" flow, and folder pickers. `folderId: ""` lands
in the Drive root, which is what every existing create path does.
