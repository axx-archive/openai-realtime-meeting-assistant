# Layouts

The whole product surface is one meeting-room layout in `index.html`.

## App Shell

Path: `index.html`

Structure:

```html
<main id="appShell">
  <header class="topbar mount-stagger">...</header>
  <div class="workspace">
    <section id="accessPanel" class="access-panel hearth-access mount-stagger">...</section>
    <aside class="scout-rail mount-stagger">...</aside>
    <section id="presentationTile" class="presentation-tile hearth-presentation mount-stagger">...</section>
    <aside id="boardRail" class="board-rail mount-stagger">...</aside>
    <div id="boardSurface" class="board-surface board-expanded-surface mount-stagger">...</div>
  </div>
  <footer class="meeting-bar mount-stagger">...</footer>
</main>
```

The room uses a warm-dark chrome around a central hearth presentation tile, a Scout/memory rail, a compact board preview, and an expanded parchment board mode.
