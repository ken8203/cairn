import { spawn } from "node:child_process";
import { writeFileSync, mkdirSync } from "node:fs";
const [dir, fps, dur] = [process.argv[2], +process.argv[3], +process.argv[4]];
mkdirSync(`${dir}/frames`, { recursive: true });
const chrome = spawn("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  ["--headless=new", "--remote-debugging-port=9333", `--user-data-dir=${dir}/chrome`, "--hide-scrollbars", "about:blank"], { stdio: "ignore" });
let tab;
for (let i = 0; i < 50 && !tab; i++) {
  await new Promise((r) => setTimeout(r, 200));
  try { tab = (await (await fetch("http://127.0.0.1:9333/json/list")).json()).find((t) => t.type === "page"); } catch {}
}
const ws = new WebSocket(tab.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r));
let id = 0; const wait = new Map();
ws.addEventListener("message", (e) => { const m = JSON.parse(e.data); if (wait.has(m.id)) { wait.get(m.id)(m.result); wait.delete(m.id); } });
const send = (method, params = {}) => new Promise((r) => { wait.set(++id, r); ws.send(JSON.stringify({ id, method, params })); });
await send("Emulation.setDeviceMetricsOverride", { width: 1200, height: 660, deviceScaleFactor: 2, mobile: false });
await send("Page.navigate", { url: `file://${dir}/demo.html#t=0` });
await new Promise((r) => setTimeout(r, 1500));
const n = Math.round(fps * dur);
for (let i = 0; i < n; i++) {
  await send("Runtime.evaluate", { expression: `setT(${(i / fps).toFixed(4)})` });
  const { data } = await send("Page.captureScreenshot", { format: "png" });
  writeFileSync(`${dir}/frames/f${String(i).padStart(4, "0")}.png`, Buffer.from(data, "base64"));
}
ws.close(); chrome.kill();
console.log("frames", n);
