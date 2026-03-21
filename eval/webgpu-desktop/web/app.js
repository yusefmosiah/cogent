const api = {
  async list(path = "") {
    const url = new URL("/api/files", location.origin);
    if (path) url.searchParams.set("path", path);
    return requestJson(url);
  },
  async read(path) {
    const url = new URL("/api/files/read", location.origin);
    url.searchParams.set("path", path);
    return requestJson(url);
  },
  async write(path, content) {
    return requestJson(new URL("/api/files/write", location.origin), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path, content }),
    });
  },
};

const state = {
  windows: [
    { id: "editor", title: "Text Editor", kind: "editor", x: 28, y: 24, w: 420, h: 360, minimized: false, closed: false, maximized: false },
    { id: "browser", title: "Web Browser", kind: "browser", x: 468, y: 28, w: 480, h: 360, minimized: false, closed: false, maximized: false },
    { id: "mine", title: "Minesweeper", kind: "minesweeper", x: 52, y: 410, w: 360, h: 360, minimized: false, closed: false, maximized: false },
    { id: "files", title: "File Browser", kind: "files", x: 440, y: 410, w: 520, h: 360, minimized: false, closed: false, maximized: false },
  ],
  nextZ: 10,
  activeId: "browser",
  rootPath: "",
};

const dom = {};

let gpuRenderer = null;

window.addEventListener("load", init);
window.addEventListener("resize", scheduleLayout);

function init() {
  dom.canvas = document.getElementById("desktop-canvas");
  dom.windows = document.getElementById("window-layer");
  dom.taskbar = document.getElementById("taskbar");
  setupTaskbar();
  setupWindows();
  initApps();
  initPointerHandling();
  initRenderer();
  scheduleLayout();
  requestAnimationFrame(loop);
}

function setupTaskbar() {
  dom.taskbar.innerHTML = `
    <div class="taskbar-shell">
      <button data-open="editor">Editor</button>
      <button data-open="browser">Browser</button>
      <button data-open="mine">Minesweeper</button>
      <button data-open="files">Files</button>
      <div class="taskbar-spacer"></div>
      <button class="secondary" data-action="tilt">Desktop</button>
    </div>
  `;
  dom.taskbar.addEventListener("click", (event) => {
    const open = event.target?.dataset?.open;
    const action = event.target?.dataset?.action;
    if (open) toggleWindow(open, true);
    if (action === "tilt") {
      for (const win of state.windows) {
        if (!win.closed) {
          win.minimized = false;
          win.maximized = false;
        }
      }
      scheduleLayout();
    }
  });
}

function setupWindows() {
  dom.windows.innerHTML = "";
  for (const win of state.windows) {
    const el = document.createElement("div");
    el.className = "window";
    el.dataset.id = win.id;
    el.innerHTML = `
      <div class="window-title">${escapeHtml(win.title)}</div>
      <div class="window-controls">
        <button data-action="minimize" title="Minimize">_</button>
        <button data-action="maximize" title="Maximize">▢</button>
        <button data-action="close" title="Close">×</button>
      </div>
      <div class="drag-zone" data-drag-zone="1"></div>
      <div class="resize-handle" data-resize-handle="1"></div>
      <div class="window-content" data-content="1"></div>
    `;
    el.addEventListener("pointerdown", () => focusWindow(win.id));
    el.querySelector("[data-action='minimize']").addEventListener("click", () => {
      win.minimized = !win.minimized;
      if (win.minimized) win.maximized = false;
      scheduleLayout();
    });
    el.querySelector("[data-action='maximize']").addEventListener("click", () => {
      if (!win.maximized) {
        win.restore = { x: win.x, y: win.y, w: win.w, h: win.h };
        win.maximized = true;
        win.minimized = false;
      } else {
        win.maximized = false;
        if (win.restore) {
          win.x = win.restore.x;
          win.y = win.restore.y;
          win.w = win.restore.w;
          win.h = win.restore.h;
        }
      }
      scheduleLayout();
    });
    el.querySelector("[data-action='close']").addEventListener("click", () => {
      win.closed = true;
      win.minimized = true;
      scheduleLayout();
    });
    dom.windows.appendChild(el);
  }
}

function initApps() {
  syncAppSizes();
}

function getWindow(id) {
  return state.windows.find((w) => w.id === id);
}

function getWindowElement(id) {
  return dom.windows.querySelector(`.window[data-id="${id}"]`);
}

function focusWindow(id) {
  const win = getWindow(id);
  if (!win || win.closed) return;
  win.z = ++state.nextZ;
  state.activeId = id;
  scheduleLayout();
}

function toggleWindow(id, forceOpen = false) {
  const win = getWindow(id);
  if (!win) return;
  if (win.closed || forceOpen) {
    win.closed = false;
    win.minimized = false;
    win.maximized = false;
  } else {
    win.minimized = !win.minimized;
  }
  if (!win.maximized && win.restore) {
    win.x = win.restore.x;
    win.y = win.restore.y;
    win.w = win.restore.w;
    win.h = win.restore.h;
  }
  focusWindow(id);
}

function scheduleLayout() {
  layoutWindows();
  drawDesktop();
}

function layoutWindows() {
  const viewportW = window.innerWidth;
  const viewportH = window.innerHeight;
  for (const win of state.windows) {
    const el = getWindowElement(win.id);
    if (!el) continue;
    if (win.closed) {
      el.style.display = "none";
      continue;
    }
    el.style.display = "block";
    if (win.maximized) {
      win.x = 0;
      win.y = 0;
      win.w = viewportW;
      win.h = viewportH - 54;
    }
    el.classList.toggle("minimized", win.minimized);
    el.classList.toggle("maximized", win.maximized);
    el.style.left = `${win.x}px`;
    el.style.top = `${win.y}px`;
    el.style.width = `${win.w}px`;
    el.style.height = `${win.h}px`;
    el.style.zIndex = String(win.z || 0);
    const content = el.querySelector("[data-content='1']");
    content.style.pointerEvents = win.minimized ? "none" : "auto";
    content.style.opacity = win.minimized ? "0" : "1";
    if (win.kind !== "browser" && win.kind !== "files") {
      content.style.background = "transparent";
    }
    if (state.activeId === win.id) {
      el.style.filter = "brightness(1.02)";
    } else {
      el.style.filter = "none";
    }
    placeChromeTitle(el, win.title);
  }
  syncAppSizes();
}

function placeChromeTitle(el, title) {
  const titleEl = el.querySelector(".window-title");
  titleEl.textContent = title;
}

function initPointerHandling() {
  let drag = null;
  let resize = null;

  dom.windows.addEventListener("pointerdown", (event) => {
    const winEl = event.target.closest?.(".window");
    if (!winEl) return;
    const id = winEl.dataset.id;
    const win = getWindow(id);
    if (!win || win.closed) return;
    if (event.target.closest("[data-drag-zone]") && !win.maximized) {
      const start = { x: event.clientX, y: event.clientY, x0: win.x, y0: win.y };
      drag = { win, start };
      focusWindow(id);
      winEl.setPointerCapture?.(event.pointerId);
      return;
    }
    if (event.target.closest("[data-resize-handle]") && !win.maximized) {
      const start = { x: event.clientX, y: event.clientY, w0: win.w, h0: win.h };
      resize = { win, start };
      focusWindow(id);
      winEl.setPointerCapture?.(event.pointerId);
    }
  });

  window.addEventListener("pointermove", (event) => {
    if (drag) {
      const { win, start } = drag;
      win.x = Math.max(0, start.x0 + (event.clientX - start.x));
      win.y = Math.max(0, start.y0 + (event.clientY - start.y));
      scheduleLayout();
    }
    if (resize) {
      const { win, start } = resize;
      win.w = clamp(start.w0 + (event.clientX - start.x), 260, window.innerWidth);
      win.h = clamp(start.h0 + (event.clientY - start.y), 220, window.innerHeight - 54);
      scheduleLayout();
    }
  });

  window.addEventListener("pointerup", () => {
    drag = null;
    resize = null;
  });
}

async function initRenderer() {
  const hasGPU = !!navigator.gpu;
  if (!hasGPU) {
    gpuRenderer = createCanvasFallbackRenderer(dom.canvas);
    return;
  }
  try {
    gpuRenderer = await createWebGPUDesktop(dom.canvas);
  } catch (error) {
    console.warn("WebGPU init failed, using fallback renderer", error);
    gpuRenderer = createCanvasFallbackRenderer(dom.canvas);
  }
}

function drawDesktop() {
  if (!gpuRenderer) return;
  gpuRenderer.draw(state.windows, state.activeId, { taskbarH: 54 });
}

function loop() {
  drawDesktop();
  requestAnimationFrame(loop);
}

async function createWebGPUDesktop(canvas) {
  const adapter = await navigator.gpu.requestAdapter();
  if (!adapter) throw new Error("No WebGPU adapter");
  const device = await adapter.requestDevice();
  const context = canvas.getContext("webgpu");
  const format = navigator.gpu.getPreferredCanvasFormat();
  context.configure({
    device,
    format,
    alphaMode: "premultiplied",
  });

  const pipeline = device.createRenderPipeline({
    layout: "auto",
    vertex: {
      module: device.createShaderModule({
        code: `
          struct VertexOutput {
            @builtin(position) position: vec4<f32>,
            @location(0) color: vec4<f32>,
          };

          struct Rect {
            min: vec2<f32>,
            max: vec2<f32>,
            color: vec4<f32>,
          };

          @group(0) @binding(0) var<uniform> rect: Rect;

          @vertex
          fn main(@builtin(vertex_index) vertexIndex: u32) -> VertexOutput {
            let corners = array<vec2<f32>, 6>(
              vec2<f32>(0.0, 0.0),
              vec2<f32>(1.0, 0.0),
              vec2<f32>(0.0, 1.0),
              vec2<f32>(0.0, 1.0),
              vec2<f32>(1.0, 0.0),
              vec2<f32>(1.0, 1.0)
            );
            let corner = corners[vertexIndex];
            let xy = mix(rect.min, rect.max, corner);
            var out: VertexOutput;
            out.position = vec4<f32>(xy.x, xy.y, 0.0, 1.0);
            out.color = rect.color;
            return out;
          }
        `,
      }),
      entryPoint: "main",
    },
    fragment: {
      module: device.createShaderModule({
        code: `
          @fragment
          fn main(@location(0) color: vec4<f32>) -> @location(0) vec4<f32> {
            return color;
          }
        `,
      }),
      entryPoint: "main",
      targets: [{ format }],
    },
    primitive: { topology: "triangle-list" },
  });

  const rectBuffer = device.createBuffer({
    size: 32,
    usage: GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST,
  });
  const bindGroup = device.createBindGroup({
    layout: pipeline.getBindGroupLayout(0),
    entries: [{ binding: 0, resource: { buffer: rectBuffer } }],
  });

  function draw(windows, activeId) {
    const width = canvas.clientWidth || window.innerWidth;
    const height = canvas.clientHeight || window.innerHeight;
    const dpr = window.devicePixelRatio || 1;
    const pixelWidth = Math.max(1, Math.floor(width * dpr));
    const pixelHeight = Math.max(1, Math.floor(height * dpr));
    if (canvas.width !== pixelWidth || canvas.height !== pixelHeight) {
      canvas.width = pixelWidth;
      canvas.height = pixelHeight;
    }
    const rects = buildRects(windows, activeId, width, height);

    const encoder = device.createCommandEncoder();
    const pass = encoder.beginRenderPass({
      colorAttachments: [{
        view: context.getCurrentTexture().createView(),
        loadOp: "clear",
        storeOp: "store",
        clearValue: { r: 0.08, g: 0.11, b: 0.18, a: 1 },
      }],
    });
    pass.setPipeline(pipeline);
    for (const rect of rects) {
      device.queue.writeBuffer(rectBuffer, 0, new Float32Array([
        rect.min[0], rect.min[1],
        rect.max[0], rect.max[1],
        rect.color[0], rect.color[1], rect.color[2], rect.color[3],
      ]));
      pass.setBindGroup(0, bindGroup);
      pass.draw(6, 1, 0, 0);
    }
    pass.end();
    device.queue.submit([encoder.finish()]);
  }

  return { draw };
}

function createCanvasFallbackRenderer(canvas) {
  const ctx = canvas.getContext("2d");
  return {
    draw(windows, activeId) {
      const dpr = window.devicePixelRatio || 1;
      const width = Math.floor(canvas.clientWidth * dpr);
      const height = Math.floor(canvas.clientHeight * dpr);
      if (canvas.width !== width) canvas.width = width;
      if (canvas.height !== height) canvas.height = height;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      const grad = ctx.createLinearGradient(0, 0, width / dpr, height / dpr);
      grad.addColorStop(0, "#0e1426");
      grad.addColorStop(1, "#1b2746");
      ctx.fillStyle = grad;
      ctx.fillRect(0, 0, width / dpr, height / dpr);
      ctx.fillStyle = "rgba(255,255,255,0.05)";
      ctx.fillRect(0, 0, width / dpr, 34);
      ctx.fillStyle = "rgba(7,12,22,0.72)";
      ctx.fillRect(16, height / dpr - 54, width / dpr - 32, 42);
      for (const win of windows) {
        if (win.closed) continue;
        ctx.fillStyle = win.id === activeId ? "rgba(88, 199, 255, 0.20)" : "rgba(255,255,255,0.10)";
        ctx.fillRect(win.x, win.y, win.w, 34);
        ctx.strokeStyle = "rgba(255,255,255,0.16)";
        ctx.strokeRect(win.x + 1, win.y + 1, win.w - 2, win.h - 2);
      }
    },
  };
}

function buildRects(windows, activeId, width, height) {
  const rects = [];
  rects.push(rect(0, 0, width, height, rgba(14, 20, 38, 1)));
  rects.push(rect(0, 0, width, 34, rgba(255, 255, 255, 0.03)));
  rects.push(rect(16, height - 54, width - 32, 42, rgba(7, 12, 22, 0.72)));
  for (const win of windows) {
    if (win.closed) continue;
    const active = win.id === activeId;
    rects.push(rect(win.x, win.y, win.w, win.h, rgba(3, 8, 15, 0.36)));
    rects.push(rect(win.x + 2, win.y + 2, win.w - 4, 32, active ? rgba(88, 199, 255, 0.22) : rgba(255, 255, 255, 0.12)));
    rects.push(rect(win.x + 2, win.y + 34, win.w - 4, win.h - 36, rgba(5, 10, 18, 0.22)));
    rects.push(rect(win.x + win.w - 18, win.y + win.h - 18, 16, 16, rgba(255, 255, 255, 0.18)));
  }
  return rects;
}

function rect(x, y, w, h, color) {
  const sx = (x / window.innerWidth) * 2 - 1;
  const ex = ((x + w) / window.innerWidth) * 2 - 1;
  const top = 1 - (y / window.innerHeight) * 2;
  const bottom = 1 - ((y + h) / window.innerHeight) * 2;
  return {
    min: [sx, bottom],
    max: [ex, top],
    color,
  };
}

function rgba(r, g, b, a) {
  return [r / 255, g / 255, b / 255, a];
}

function syncAppSizes() {
  const editor = getWindowElement("editor")?.querySelector("[data-content='1']");
  if (editor && !editor.dataset.ready) {
    editor.innerHTML = `
      <div class="app-pane">
        <div class="toolbar">
          <button id="editor-save">Save</button>
          <button class="secondary" id="editor-reset">Reset</button>
          <span class="status-line" id="editor-status">Ready</span>
        </div>
        <textarea class="editor-area" spellcheck="false"></textarea>
      </div>
    `;
    editor.dataset.ready = "1";
    const textarea = editor.querySelector("textarea");
    const status = editor.querySelector("#editor-status");
    textarea.value = loadEditorDraft();
    textarea.addEventListener("input", () => {
      status.textContent = "Unsaved changes";
      localStorage.setItem("webgpu-desktop.editor", textarea.value);
    });
    editor.querySelector("#editor-save").addEventListener("click", async () => {
      localStorage.setItem("webgpu-desktop.editor", textarea.value);
      await api.write("notes/editor-draft.txt", textarea.value);
      status.textContent = "Saved to notes/editor-draft.txt";
    });
    editor.querySelector("#editor-reset").addEventListener("click", () => {
      textarea.value = starterEditorText();
      localStorage.setItem("webgpu-desktop.editor", textarea.value);
      status.textContent = "Loaded starter note";
    });
  }

  const browser = getWindowElement("browser")?.querySelector("[data-content='1']");
  if (browser && !browser.dataset.ready) {
    browser.innerHTML = `
      <div class="app-pane">
        <div class="toolbar">
          <input class="browser-url" type="text" value="about:blank" />
          <button id="browser-go">Go</button>
          <button class="secondary" id="browser-home">Home</button>
        </div>
        <iframe class="browser-frame" sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts"></iframe>
      </div>
    `;
    browser.dataset.ready = "1";
    const frame = browser.querySelector("iframe");
    const url = browser.querySelector(".browser-url");
    const go = async () => {
      const value = url.value.trim() || "about:blank";
      if (value === "home" || value === "/") {
        frame.srcdoc = browserHomeHtml();
        return;
      }
      if (value.startsWith("about:") || value.startsWith("http://") || value.startsWith("https://") || value.startsWith("/")) {
        frame.src = value.startsWith("/") ? new URL(value, location.origin).href : value;
        return;
      }
      frame.src = `https://${value}`;
    };
    browser.querySelector("#browser-go").addEventListener("click", go);
    browser.querySelector("#browser-home").addEventListener("click", () => {
      url.value = "home";
      go();
    });
    url.addEventListener("keydown", (event) => {
      if (event.key === "Enter") go();
    });
    frame.srcdoc = browserHomeHtml();
  }

  const mine = getWindowElement("mine")?.querySelector("[data-content='1']");
  if (mine && !mine.dataset.ready) {
    mine.innerHTML = `
      <div class="app-pane">
        <div class="toolbar">
          <button id="mine-reset">New Game</button>
          <span class="status-line" id="mine-status">Find all the mines.</span>
        </div>
        <div class="mine-grid" id="mine-grid"></div>
      </div>
    `;
    mine.dataset.ready = "1";
    initMinesweeper(mine);
  }

  const files = getWindowElement("files")?.querySelector("[data-content='1']");
  if (files && !files.dataset.ready) {
    files.innerHTML = `
      <div class="app-pane">
        <div class="toolbar">
          <button id="files-refresh">Refresh</button>
          <button class="secondary" id="files-up">Up</button>
          <span class="status-line" id="files-status">Loading files...</span>
        </div>
        <div class="file-layout">
          <div class="file-list" id="file-list"></div>
          <div class="file-editor">
            <div class="file-meta" id="file-meta">No file selected</div>
            <textarea id="file-content" spellcheck="false"></textarea>
            <div class="toolbar">
              <button id="file-save">Save</button>
              <span class="status-line" id="file-save-status"></span>
            </div>
          </div>
        </div>
      </div>
    `;
    files.dataset.ready = "1";
    initFileBrowser(files);
  }
}

function loadEditorDraft() {
  return localStorage.getItem("webgpu-desktop.editor") || starterEditorText();
}

function starterEditorText() {
  return [
    "Welcome to the WebGPU desktop.",
    "",
    "Use this editor to draft notes, then save them into the backend workspace.",
    "The file browser can confirm the saved document exists.",
    "",
    "Try:",
    "- edit this text",
    "- save to notes/editor-draft.txt",
    "- open the file browser",
  ].join("\n");
}

function browserHomeHtml() {
  return `<!doctype html>
  <html><head><meta charset="utf-8"><style>
  body{margin:0;font-family:system-ui;background:linear-gradient(135deg,#0f172a,#172554);color:#eef2ff;display:grid;place-items:center;height:100vh}
  .card{max-width:480px;padding:24px;border-radius:20px;background:rgba(255,255,255,.08);border:1px solid rgba(255,255,255,.14)}
  h1{margin-top:0}
  code{background:rgba(255,255,255,.1);padding:2px 6px;border-radius:6px}
  </style></head>
  <body><div class="card">
    <h1>Browser Home</h1>
    <p>This iframe browser supports local URLs, data URLs, and external sites that permit embedding.</p>
    <p>Try <code>/</code>, <code>about:blank</code>, or <code>https://example.com</code>.</p>
  </div></body></html>`;
}

function initMinesweeper(root) {
  const size = { rows: 9, cols: 9, mines: 10 };
  const state = createMineState(size.rows, size.cols, size.mines);
  const grid = root.querySelector("#mine-grid");
  const status = root.querySelector("#mine-status");
  const reset = root.querySelector("#mine-reset");
  const buttons = [];

  function render() {
    grid.style.gridTemplateColumns = `repeat(${size.cols}, 28px)`;
    grid.innerHTML = "";
    buttons.length = 0;
    for (let r = 0; r < size.rows; r++) {
      for (let c = 0; c < size.cols; c++) {
        const cell = document.createElement("button");
        cell.className = "cell";
        const tile = state.tiles[r][c];
        cell.textContent = tile.revealed ? (tile.mine ? "💣" : tile.neighborCount || "") : tile.flagged ? "⚑" : "";
        cell.classList.toggle("revealed", tile.revealed);
        cell.classList.toggle("flagged", tile.flagged);
        cell.addEventListener("click", () => {
          if (state.over) return;
          revealCell(state, r, c);
          render();
          syncMineStatus();
        });
        cell.addEventListener("contextmenu", (event) => {
          event.preventDefault();
          if (state.over || tile.revealed) return;
          tile.flagged = !tile.flagged;
          render();
          syncMineStatus();
        });
        grid.appendChild(cell);
        buttons.push(cell);
      }
    }
  }

  function syncMineStatus() {
    if (state.won) {
      status.textContent = "You cleared the board.";
    } else if (state.over) {
      status.textContent = "Boom. Reset to try again.";
    } else {
      status.textContent = `${state.minesLeft} mines left.`;
    }
  }

  reset.addEventListener("click", () => {
    const fresh = createMineState(size.rows, size.cols, size.mines);
    Object.assign(state, fresh);
    render();
    syncMineStatus();
  });

  render();
  syncMineStatus();
}

function createMineState(rows, cols, mines) {
  const tiles = Array.from({ length: rows }, () =>
    Array.from({ length: cols }, () => ({ mine: false, revealed: false, flagged: false, neighborCount: 0 }))
  );
  const all = [];
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) all.push([r, c]);
  shuffle(all);
  for (let i = 0; i < mines; i++) {
    const [r, c] = all[i];
    tiles[r][c].mine = true;
  }
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      tiles[r][c].neighborCount = countNeighbors(tiles, r, c);
    }
  }
  return { rows, cols, mines, tiles, over: false, won: false, minesLeft: mines };
}

function revealCell(state, row, col) {
  const tile = state.tiles[row][col];
  if (tile.revealed || tile.flagged) return;
  tile.revealed = true;
  if (tile.mine) {
    state.over = true;
    revealAll(state);
    return;
  }
  if (tile.neighborCount === 0) {
    for (let r = row - 1; r <= row + 1; r++) {
      for (let c = col - 1; c <= col + 1; c++) {
        if (state.tiles[r]?.[c]) revealCell(state, r, c);
      }
    }
  }
  if (checkWon(state)) {
    state.won = true;
    state.over = true;
  }
}

function revealAll(state) {
  for (const row of state.tiles) {
    for (const tile of row) tile.revealed = true;
  }
}

function checkWon(state) {
  for (const row of state.tiles) {
    for (const tile of row) {
      if (!tile.mine && !tile.revealed) return false;
    }
  }
  return true;
}

function countNeighbors(tiles, row, col) {
  let count = 0;
  for (let r = row - 1; r <= row + 1; r++) {
    for (let c = col - 1; c <= col + 1; c++) {
      if (r === row && c === col) continue;
      if (tiles[r]?.[c]?.mine) count++;
    }
  }
  return count;
}

function shuffle(values) {
  for (let i = values.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [values[i], values[j]] = [values[j], values[i]];
  }
}

async function initFileBrowser(root) {
  const list = root.querySelector("#file-list");
  const meta = root.querySelector("#file-meta");
  const content = root.querySelector("#file-content");
  const status = root.querySelector("#files-status");
  const saveStatus = root.querySelector("#file-save-status");
  const refresh = root.querySelector("#files-refresh");
  const up = root.querySelector("#files-up");
  const save = root.querySelector("#file-save");

  const app = {
    cwd: "",
    selected: "",
    entries: [],
  };

  async function loadDir(path = app.cwd) {
    status.textContent = "Loading files...";
    const data = await api.list(path);
    app.cwd = data.path || "";
    app.entries = data.entries || [];
    renderList();
    status.textContent = `Directory: ${app.cwd || "/"}`;
  }

  async function openEntry(entry) {
    if (entry.isDir) {
      await loadDir(entry.path);
      return;
    }
    app.selected = entry.path;
    const data = await api.read(entry.path);
    meta.textContent = data.path;
    content.value = data.content;
    saveStatus.textContent = "Loaded";
  }

  function renderList() {
    list.innerHTML = "";
    if (app.cwd) {
      const parent = document.createElement("button");
      parent.className = "secondary";
      parent.textContent = "..";
      parent.addEventListener("click", async () => {
        const parts = app.cwd.split("/").filter(Boolean);
        parts.pop();
        await loadDir(parts.join("/"));
      });
      list.appendChild(parent);
    }
    for (const entry of app.entries) {
      const item = document.createElement("button");
      item.textContent = `${entry.isDir ? "📁" : "📄"} ${entry.name}`;
      item.title = entry.path;
      item.className = entry.isDir ? "secondary" : "";
      item.addEventListener("click", () => openEntry(entry));
      list.appendChild(item);
    }
  }

  refresh.addEventListener("click", () => loadDir());
  up.addEventListener("click", async () => {
    const parts = app.cwd.split("/").filter(Boolean);
    parts.pop();
    await loadDir(parts.join("/"));
  });
  save.addEventListener("click", async () => {
    if (!app.selected) {
      saveStatus.textContent = "Open a file first.";
      return;
    }
    await api.write(app.selected, content.value);
    saveStatus.textContent = "Saved";
    status.textContent = `Saved ${app.selected}`;
  });
  content.addEventListener("input", () => {
    saveStatus.textContent = "Unsaved changes";
  });

  await loadDir();
}

function createMineGamePlaceholder() {}

function clamp(v, min, max) {
  return Math.max(min, Math.min(max, v));
}

function escapeHtml(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

async function requestJson(url, options = {}) {
  const response = await fetch(url, options);
  const text = await response.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = text;
  }
  if (!response.ok) {
    const message = payload && payload.error ? payload.error : response.statusText;
    throw new Error(message);
  }
  return payload;
}
