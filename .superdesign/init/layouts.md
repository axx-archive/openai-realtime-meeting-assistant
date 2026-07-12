# Layouts

The current product is a single-file, multi-tool BonfireOS shell in `index.html`.

## Authenticated shell

```html
<main id="appShell" data-tool="office">
  <nav id="toolRail" class="tool-rail">...</nav>
  <header class="topbar">...</header>
  <div class="workspace">
    <section id="officeTool">...</section>
    <section id="presentationTile" class="presentation-tile">...</section>
    <section id="chatTool">...</section>
    <section id="memoryTool">...</section>
    <section id="filesTool">...</section>
    <section id="artifactsTool">...</section>
    <!-- board, research, design, and grill states reuse this shell -->
  </div>
  <footer class="meeting-bar">...</footer>
</main>
```

Desktop uses a fixed 60px left tool rail. The top bar is intentionally quiet. Mobile converts the tool rail into a centered, safe-area-aware bottom glass island.

## Room layout

`#presentationTile` contains the stage chrome, `#hearthStage`, screen-share stage, and canonical `#videoStack` participant tiles. The live layout state is:

- `screen-share` when a share is active;
- `pinned` when a participant is explicitly pinned;
- `grid` otherwise.

On mobile, normal room layout is one speaker hero plus a participant filmstrip. Layout changes must toggle classes on canonical tiles rather than moving or duplicating video elements.

## Contextual call chrome

`.meeting-bar` owns mic, camera, recording, room chat, invite, notes, and leave actions. On phones, secondary desktop controls such as screen share, board, device selects, and mixer are hidden or routed to appropriate sheets. Call chrome and global navigation must respect the home indicator and must not cover the stage or each other.
