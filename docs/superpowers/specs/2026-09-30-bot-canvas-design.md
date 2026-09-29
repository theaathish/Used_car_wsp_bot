# Design Specification: Node-Based Canvas Editor for Bot Configuration

**Date:** 2026-09-30  
**Status:** Approved  
**Topic:** Visual Node-Based Flowchart Editor for Bot Configuration  

---

## 1. Overview & Objective

AutoKart's dynamic WhatsApp Bot Rule Engine allows dealership admins to configure conversation flows, questions, validations, and branching logic entirely via the database without code changes.

To elevate user experience, this feature introduces an **interactive node-based canvas editor** (similar to Voiceflow or Botpress) inside the Admin Panel's **Bot Configuration** module. Dealership admins can visually inspect questions as node cards, drag them around a pan-and-zoom infinite grid, visually connect output ports to input ports to set transitions and branching conditions, and edit question details via an inspector drawer.

---

## 2. Technical Stack & Component Architecture

### 2.1 Dependencies
- **Drawflow Library**:
  - `https://cdn.jsdelivr.net/gh/jerosoler/Drawflow/dist/drawflow.min.js`
  - `https://cdn.jsdelivr.net/gh/jerosoler/Drawflow/dist/drawflow.min.css`
- Lightweight (~20KB), pure Vanilla JS, zero build step required.
- Custom CSS skin extending AutoKart's design tokens:
  - Dot-grid infinite canvas background (`#eef1f6` / `#e2e8f0`).
  - Node cards with rounded corners, subtle shadows, status pills, and typography matching the admin dashboard.
  - Distinct SVG bezier curve colors:
    - Blue wires for standard sequential transitions (`next_question_id`).
    - Amber/purple wires for conditional branching rules (`bot_conditions`).

### 2.2 UI Layout & DOM Structure
Located in `<section id="s-bot">` of [`web/dist/index.html`](file:///Users/user/Workspace/Projects/Strucureo_Projects/Harry_projects/sellingbot/web/dist/index.html):
- **View Toggle Bar**:
  - `[Canvas View]` / `[Table View]` switcher.
- **Canvas Toolbar**:
  - Flow Selector: dropdown selecting active flow (`Buy a Car`, `Sell a Car`, etc.).
  - `+ Add Question` button.
  - `Auto-Arrange` button (arranges nodes along flow sequence).
  - Zoom controls: `[ + ]`, `[ - ]`, `[ 100% ]`.
  - Filter / Search box.
- **Canvas Workspace (`#drawflow`)**:
  - Interactive infinite viewport with pan and zoom.
- **Inspector Drawer (`#bot_canvas_drawer`)**:
  - Slides in from the right when a node is clicked or created.
  - Live editing of:
    - Question text (sent on WhatsApp)
    - Field name (e.g. `vehicle_type`, `budget_max`)
    - Question type (`text`, `number`, `select`, `boolean`, `phone`, `email`)
    - Allowed values (for `select` type)
    - Validation rules & error message
    - Required toggle & Order index
    - Branching condition rules list & add form
    - Delete Question button

---

## 3. Data Mapping & Graph Synchronization

### 3.1 Node Representation
Each question in `bot_questions` is converted into a Drawflow node:
- **Node ID**: mapped 1:1 with `bot_questions.id`.
- **Node Header**:
  - Order index badge (`#1`, `#2`, etc.).
  - Field name badge (`budget_max`, `vehicle_type`).
  - Type badge (`SELECT`, `NUMBER`, etc.).
- **Node Body**:
  - Truncated preview of the WhatsApp question text.
- **Ports**:
  - **Inputs (Left)**: 1 input port accepting incoming connections.
  - **Outputs (Right)**:
    - Port 1 (Top right): Default `Next Question` output.
    - Additional Ports (Bottom right): One port per configured `bot_condition` (labeled with operator and value, e.g., `= "Any"`).

### 3.2 Edge & Wire Routing
Connections represent state transitions:
1. **Default Next Connection**:
   - Origin: Node's default output port.
   - Destination: Next node's input port.
   - Action on Connect: Calls `PUT /api/bot/questions/:source_id` with `{ next_question_id: target_id }`.
   - Action on Disconnect: Calls `PUT /api/bot/questions/:source_id` with `{ next_question_id: null }`.
2. **Branching Condition Connection**:
   - Origin: Node's condition output port.
   - Destination: Target node's input port.
   - Action on Connect: Calls `PUT /api/bot/conditions/:condition_id` or creates condition with `target_question_id: target_id`.
   - Action on Disconnect: Clears `target_question_id` or removes condition.

### 3.3 Flow State & Persistence
- When a flow is selected, Drawflow is initialized or cleared:
  1. Fetch questions via `GET /api/bot/flows/:flowId/questions`.
  2. Fetch conditions for each question.
  3. Calculate auto-layout coordinates:
     - Root/Entry question at `(100, 200)`.
     - Subsequent questions placed horizontally with `dx = 340px`, branching vertically with `dy = 180px`.
  4. Render nodes and draw connecting wires between source outputs and destination inputs.

---

## 4. User Interaction & Workflows

1. **Viewing a Flow as a Visual Graph**:
   - Admin navigates to **Bot Config**, views **Canvas View**.
   - Selects "Buy a Car": sees the 7 questions arranged in sequence with curved wires showing the path, and an amber branch wire skipping from `vehicle_type = Any` directly to `brand`.
2. **Adding a New Question Node**:
   - Admin clicks `+ Add Question`.
   - Node appears on canvas, inspector drawer opens automatically.
   - Admin fills in question text and field name, clicks `Save`.
   - Node title and badge update dynamically on canvas.
3. **Connecting Questions**:
   - Admin drags from output socket of Question A to input socket of Question B.
   - Wire snaps into place. Toast notification confirms "Connection saved".
   - Bot rule engine immediately reflects the new `next_question_id`.
4. **Editing Existing Questions**:
   - Admin clicks on a node.
   - Node highlights with active border, inspector drawer slides in with pre-filled inputs.
   - Admin updates validation rule or question text, clicks `Save`.
5. **Deleting Connections or Nodes**:
   - Selecting a wire and pressing Backspace / Delete prompts confirmation and disconnects the route.
   - Clicking `Delete Question` in inspector confirms and calls `DELETE /api/bot/questions/:id`, removing the node and all connected wires.
6. **Switching to Table View**:
   - Clicking `Table View` reveals the existing tabular interface without page reload, ensuring users who prefer form/table entry retain full access.

---

## 5. Security & Validation

- All modifications pass through the existing authenticated `/api/bot/*` endpoints requiring an `admin` role JWT token.
- Input validation on question fields and condition operators remains strictly enforced server-side.
- Node deletion cascades gracefully via database foreign key constraints (`ON DELETE SET NULL` on `next_question_id`).

---

## 6. Testing Strategy

1. **Unit & API Compatibility**:
   - Existing Go unit test suite (`go test ./...`) continues to pass without alterations to backend state engine.
2. **Frontend Canvas Tests**:
   - Verification of Drawflow library initialization and DOM rendering.
   - Verification of node generation from `bot_questions`.
   - Verification of connection creation updating `next_question_id`.
   - Verification of drawer opening, saving edits, and updating node elements.
3. **End-to-End Simulation**:
   - Modify a connection in canvas view -> send simulated message -> assert bot engine follows the updated path.
