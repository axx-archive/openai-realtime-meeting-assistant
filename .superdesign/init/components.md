# Components

This app is a single-file HTML client (`index.html`) backed by a Go WebRTC server. There is no component directory, framework, or shared UI primitive library. Reusable UI primitives are CSS classes and DOM factory functions inside `index.html`.

## CSS Primitives

- `btn`, `btn--primary`, `btn--ghost`, `btn--danger`, `btn--text` — button system.
- `field`, `device-control`, `device-select`, `assistant-input` — form controls.
- `pill`, `status-pill`, `card-id-pill` — status and metadata pills.
- `card`, `column`, `tags`, `owner-avatar`, `monogram` — board and avatar primitives.
- `assistant-message`, `memory-item`, `toast`, `comment-preview`, `card-detail` — live room feedback surfaces.
- `video-tile`, `hearth-seat`, `tile-avatar`, `media-flags` — participant media surfaces.

## DOM Factory Functions

```js
function renderCard(card, moved = false, completed = false)
function renderOwnerAvatar(card, options = {})
function renderIdenticon(seed)
function renderAvatarStack(container, names, options = {})
function renderMediaFlags()
function renderAssistantMessage(entry, entering = false)
function renderMemoryEntry(entry)
function renderTagList(card)
function renderPreviewDetail(labelText, value)
function renderFormField(labelText, type, value)
function renderSelectField(labelText, options, value)
```

The implementation source of record is `index.html`.
