# Bot Canvas: Node-Based Flowchart Editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an interactive, node-based flowchart canvas editor for Bot Configuration using Drawflow with an inspector drawer, auto-layout, wire transitions, and seamless toggle between Canvas and Table views.

**Architecture:** Integrate Drawflow CDN into `web/dist/index.html` with custom AutoKart styling. In `web/dist/app.js`, manage canvas lifecycle, convert `bot_questions` and `bot_conditions` into interactive nodes and wires, synchronize wire connections with `next_question_id` via the `/api/bot/*` endpoints, and provide a slide-over inspector drawer for live node edits.

**Tech Stack:** JavaScript (ES6 Vanilla), Drawflow (~20KB via CDN), HTML5/CSS3, Go monolith backend.

**Spec:** `docs/superpowers/specs/2026-09-30-bot-canvas-design.md`

## Global Constraints

- Preserve fast single-page application performance (no bulky build steps or external bundlers).
- Use curated CSS variables and responsive design matching AutoKart's existing design tokens.
- All canvas mutations must sync with existing authenticated `/api/bot/*` endpoints with zero breaking backend changes.
- Seamless toggle: dealership admins can switch freely between **Canvas View** and **Table View** without page refresh.

## Review Focus

- **Node ID Synchronization**: Ensure Drawflow numeric node IDs map reliably to and from UUID strings of `bot_questions`.
- **Wire Disconnection Handling**: Removing an edge must safely clear `next_question_id` without breaking neighboring connections.
- **Canvas View Resizing**: Canvas must properly compute dimensions when switching from hidden tabs or toggling full screen.
- **Form Sanitization in Drawer**: Allowed values must be correctly formatted to and from JSON arrays when editing `select` questions.
- **Browser Compatibility**: Zero console errors when Drawflow scripts and styles load from CDN.

---

### Task 1: HTML & CSS Scaffolding for Bot Canvas & Drawflow Assets

**Files:**
- Modify: `web/dist/index.html` (include Drawflow CDN and canvas DOM structure)

**Interfaces:**
- Produces: `#drawflow` canvas element, view toggle buttons (`btn-bot-view-canvas`, `btn-bot-view-table`), canvas toolbar (`#bot_canvas_toolbar`), inspector drawer (`#bot_canvas_drawer`).

- [ ] **Step 1: Add Drawflow CDN styles and scripts to `index.html`**

In `<head>` of `index.html`:
```html
<link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/jerosoler/Drawflow/dist/drawflow.min.css"/>
<script src="https://cdn.jsdelivr.net/gh/jerosoler/Drawflow/dist/drawflow.min.js"></script>
```

- [ ] **Step 2: Add custom CSS tokens and rules for Drawflow in `index.html`**

Add CSS for:
- Infinite dot-grid background for `#drawflow`
- Node card design matching `.panel` (`background: #ffffff`, border, box-shadow, rounded corners)
- Node header with badges for order, field name, and question type
- Sockets (`.drawflow .drawflow-node .input`, `.output`) styled with crisp borders and hover effects
- SVG connection wires with curved styling and distinct colors for default and conditional links
- Slide-over inspector drawer (`#bot_canvas_drawer`) positioned on the right of `#drawflow`

- [ ] **Step 3: Add View Toggle, Toolbar, and Canvas DOM into `#s-bot`**

Replace the static sub-tabs in `<section id="s-bot">` with:
- Top bar with Flow Selector and View Toggle: `[Canvas View]` and `[Table View]`.
- `#bot-view-canvas`: contains Toolbar (`+ Add Question`, `Auto-Arrange`, `Zoom In`, `Zoom Out`, `Reset 100%`), `#drawflow` viewport, and `#bot_canvas_drawer`.
- `#bot-view-table`: wraps the existing tabular flows, questions, conditions, and system responses sections.

- [ ] **Step 4: Verify HTML structure and syntax**

Run: `node -c web/dist/app.js` and verify `index.html` renders cleanly without layout disruption.

- [ ] **Step 5: Commit**

```bash
git add web/dist/index.html
git commit -m "feat(canvas): add Drawflow assets and bot canvas DOM scaffolding"
```

---

### Task 2: Drawflow Canvas Engine Initialization & Auto-Layout in `app.js`

**Files:**
- Modify: `web/dist/app.js`

**Interfaces:**
- Consumes: Drawflow global library (`window.Drawflow`), `/api/bot/flows/:id/questions`
- Produces: `initDrawflow()`, `renderBotCanvas(flowId)`, `autoArrangeCanvas()`, zoom/pan controls

- [ ] **Step 1: Implement `initDrawflow()`**

Create and configure the `Drawflow` instance attached to `document.getElementById('drawflow')`:
- Set `editor.reroute = true` for curved wires.
- Attach zoom level limits (`0.4` to `1.8`).
- Register custom event listeners.

- [ ] **Step 2: Implement auto-layout coordinate calculation**

Calculate `(x, y)` positions for questions:
- Start root question at `(100, 200)`.
- Step horizontally with `dx = 320` and vertical offsets for branched questions.

- [ ] **Step 3: Implement `renderBotCanvas(flowId)`**

- Clear existing Drawflow module.
- Fetch flow questions via `api('GET', '/api/bot/flows/' + flowId + '/questions')`.
- For each question, construct the custom node HTML:
  - Header: `#<order> <field_name>` + `<type pill>`.
  - Body: WhatsApp message text preview snippet.
  - Sockets: 1 input on left, 1 default output on right, plus extra conditional output sockets if conditions exist.
- Add node to editor using `editor.addNode()`.
- Map Drawflow internal node IDs to `bot_questions.id`.

- [ ] **Step 4: Implement toolbar actions**

- `zoomIn()` / `zoomOut()` / `zoomReset()`
- `autoArrangeCanvas()`: recalculates positions and repositions nodes.
- View switcher: toggles between `#bot-view-canvas` and `#bot-view-table`.

- [ ] **Step 5: Verify canvas rendering**

Run: `node -c web/dist/app.js`.

- [ ] **Step 6: Commit**

```bash
git add web/dist/app.js
git commit -m "feat(canvas): initialize Drawflow and implement auto-layout node rendering"
```

---

### Task 3: Visual Connections & Backend Synchronization

**Files:**
- Modify: `web/dist/app.js`

**Interfaces:**
- Consumes: Drawflow events `connectionCreated`, `connectionRemoved`, `/api/bot/questions/:id`
- Produces: Automatic persistence of transitions when wires are drawn or removed

- [ ] **Step 1: Render existing transitions as canvas wires**

In `renderBotCanvas(flowId)`:
- After all nodes are mounted, iterate over questions.
- If `q.next_question_id` is set, call `editor.addConnection(sourceNodeId, targetNodeId, 'output_1', 'input_1')`.
- If conditions point to `target_question_id`, connect the corresponding condition output socket to the target input socket.

- [ ] **Step 2: Handle `connectionCreated` event**

When user drags a wire between two sockets:
- Identify source question UUID and target question UUID.
- If origin is default output socket (`output_1`):
  - Call `api('PUT', '/api/bot/questions/' + sourceId, { next_question_id: targetId })`.
  - Show toast: `"Connected: Next question updated"`.
- If origin is a condition socket:
  - Update condition's `target_question_id` via `/api/bot/conditions/:id`.

- [ ] **Step 3: Handle `connectionRemoved` event**

When user deletes a wire:
- If origin was default output socket:
  - Call `api('PUT', '/api/bot/questions/' + sourceId, { next_question_id: null })`.
  - Show toast: `"Connection removed"`.
- If origin was a condition socket:
  - Update condition to remove `target_question_id`.

- [ ] **Step 4: Verify connection persistence**

Verify that drawing and removing connections correctly update database state and survive canvas reloads.

- [ ] **Step 5: Commit**

```bash
git add web/dist/app.js
git commit -m "feat(canvas): wire connections between nodes with live API persistence"
```

---

### Task 4: Inspector Drawer & Interactive Node CRUD

**Files:**
- Modify: `web/dist/index.html`
- Modify: `web/dist/app.js`

**Interfaces:**
- Consumes: Drawflow `nodeSelected`, `nodeUnselected`
- Produces: `openCanvasDrawer(questionId)`, `saveCanvasDrawer()`, `addCanvasQuestion()`, `deleteCanvasQuestion()`

- [ ] **Step 1: Implement `nodeSelected` handler & drawer opening**

When user clicks a node on canvas:
- Fetch full question data and associated conditions.
- Populate drawer inputs: `question_text`, `field_name`, `question_type`, `validation_rule`, `allowed_values`, `error_message`, `is_required`, `order_index`.
- Slide open `#bot_canvas_drawer`.
- Highlight selected node.

- [ ] **Step 2: Implement drawer Save action**

When user edits fields in drawer and clicks `Save Changes`:
- Call `api('PUT', '/api/bot/questions/' + id, payload)`.
- Update the node's visual title, field name, type badge, and text snippet on the canvas directly without full reload.
- Toast `"Question updated"`.

- [ ] **Step 3: Implement Quick `+ Add Question` on Canvas**

When user clicks `+ Add Question` on the canvas toolbar:
- Compute optimal `(x, y)` location near the last node.
- Create question via `api('POST', '/api/bot/flows/' + flowId + '/questions', defaultPayload)`.
- Add new node directly to Drawflow canvas and open the inspector drawer immediately for editing.

- [ ] **Step 4: Implement Delete Question from Canvas**

In inspector drawer, provide `Delete Question`:
- Confirm prompt.
- Call `api('DELETE', '/api/bot/questions/' + id)`.
- Remove node from Drawflow editor via `editor.removeNodeId('node-' + nodeId)`.
- Close drawer and toast `"Question deleted"`.

- [ ] **Step 5: Verify Drawer & CRUD functionality**

Verify creating a question, editing properties, and deleting questions directly through the canvas.

- [ ] **Step 6: Commit**

```bash
git add web/dist/index.html web/dist/app.js
git commit -m "feat(canvas): add inspector drawer and interactive node CRUD"
```

---

### Task 5: End-to-End Testing, Polish & Production Deployment

**Files:**
- Modify: `web/dist/app.js`
- Modify: `web/dist/index.html`

**Interfaces:**
- Consumes: Full admin stack, Go backend
- Produces: Verified, production-ready node canvas editor on Railway

- [ ] **Step 1: Test canvas toggle & responsive resize**

- Switch from Table View to Canvas View: verify canvas calculates bounding box and renders correctly without graphical clipping.
- Test zoom/pan and auto-arrange with 7+ nodes (Buy a Car flow).

- [ ] **Step 2: Run backend tests and frontend syntax validation**

```bash
node -c web/dist/app.js
go test -count=1 ./...
go build ./cmd/server
```
Expected: All pass with 0 errors.

- [ ] **Step 3: Commit and Push to `origin main`**

```bash
git add -A
git commit -m "feat: complete interactive node-based canvas for bot configuration"
git push origin main
```
Expected: Pushed to GitHub and deployed to Railway.
