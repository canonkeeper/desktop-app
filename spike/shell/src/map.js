// SPDX-License-Identifier: AGPL-3.0-only
// Probe page of the desktop shell: lays out a saga-sized graph in a worker, renders it with
// sigma (WebGL) and reports to the shell what the WebView did: WebGL renderer, drawn pixels,
// timings and every error. The graph is synthetic; names are from public-domain novels.
import Graph from "graphology";
import Sigma from "sigma";

const errors = [];
const started = performance.now();
const report = { href: location.href, userAgent: navigator.userAgent, errors };
window.addEventListener("error", (event) => errors.push(`error: ${event.message} at ${event.filename}:${event.lineno}`));
window.addEventListener("unhandledrejection", (event) => errors.push(`rejection: ${event.reason}`));
const consoleError = console.error.bind(console);
console.error = (...args) => {
  errors.push(`console.error: ${args.map(String).join(" ")}`);
  consoleError(...args);
};

function send(stage) {
  report.stage = stage;
  report.ms_since_navigation = Math.round(performance.now());
  report.ms_since_script = Math.round(performance.now() - started);
  report.epoch_ms = Date.now();
  const invoke = window.__TAURI__?.core?.invoke;
  if (invoke) {
    invoke("report", { payload: structuredClone(report) }).catch((error) => errors.push(`report: ${error}`));
  }
}

function webglInfo() {
  const canvas = document.createElement("canvas");
  const gl = canvas.getContext("webgl2") || canvas.getContext("webgl");
  if (!gl) return { available: false };
  const debug = gl.getExtension("WEBGL_debug_renderer_info");
  return {
    available: true,
    version: gl.getParameter(gl.VERSION),
    renderer: debug ? gl.getParameter(debug.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER),
    vendor: debug ? gl.getParameter(debug.UNMASKED_VENDOR_WEBGL) : gl.getParameter(gl.VENDOR),
  };
}

const names = ["Пьер", "Наташа", "Андрей", "Мария", "Николай", "Соня", "Кутузов", "Долохов", "Элен", "Анатоль",
  "Алёша", "Дмитрий", "Иван", "Грушенька", "Катерина", "Смердяков", "Зосима", "Остап", "Киса", "Шура"];

function sagaGraph(nodes, edges) {
  const graph = new Graph();
  let seed = 7;
  const random = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  for (let i = 0; i < nodes; i++) {
    graph.addNode(`n${i}`, {
      label: `${names[i % names.length]} ${Math.floor(i / names.length) + 1}`,
      x: random() * 100, y: random() * 100, size: 3 + random() * 9,
      color: ["#7a4b2a", "#2a5d7a", "#5d7a2a", "#7a2a5d"][i % 4],
    });
  }
  while (graph.size < edges) {
    const a = `n${Math.floor(random() * nodes)}`;
    const b = `n${Math.floor(random() * nodes)}`;
    if (a !== b && !graph.hasEdge(a, b)) graph.addEdge(a, b, { size: 1 + random() * 2, color: "#c9c2b6" });
  }
  return graph;
}

async function main() {
  report.dom_ready_ms = Math.round(performance.now());
  report.webgl = webglInfo();
  send("dom");
  const graph = sagaGraph(400, 900);
  const laidOut = await new Promise((resolve, reject) => {
    const worker = new Worker(new URL("./layout.js", import.meta.url), { type: "module" });
    worker.onmessage = (event) => resolve(event.data);
    worker.onerror = (event) => reject(new Error(`worker: ${event.message}`));
    worker.postMessage({ graph: graph.export(), iterations: 300 });
  });
  report.layout_ms = Math.round(laidOut.ms);
  const container = document.getElementById("map");
  const renderer = new Sigma(Graph.from(laidOut.graph), container, { renderLabels: true });
  await new Promise((resolve) => renderer.once("afterRender", resolve));
  report.render_ms = Math.round(performance.now());
  const canvases = [...container.querySelectorAll("canvas")];
  report.canvases = canvases.length;
  // Pixels of the WebGL layers right after a render: proof that the map drew something.
  let drawn = 0;
  for (const canvas of canvases) {
    const gl = canvas.getContext("webgl2") || canvas.getContext("webgl");
    if (!gl) continue;
    renderer.refresh();
    const width = gl.drawingBufferWidth;
    const height = gl.drawingBufferHeight;
    const pixels = new Uint8Array(width * height * 4);
    gl.readPixels(0, 0, width, height, gl.RGBA, gl.UNSIGNED_BYTE, pixels);
    for (let i = 3; i < pixels.length; i += 4) if (pixels[i] !== 0) drawn++;
  }
  report.drawn_pixels = drawn;
  document.getElementById("status").textContent =
    `Узлов: ${renderer.getGraph().order}, связей: ${renderer.getGraph().size}, WebGL: ${report.webgl.renderer ?? "нет"}`;
  send("rendered");
}

main().catch((error) => {
  errors.push(`main: ${error?.stack ?? error}`);
  document.getElementById("status").textContent = `Ошибка: ${error}`;
  send("failed");
});
