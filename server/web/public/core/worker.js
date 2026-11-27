// Runs Requara's engine (Go compiled to WebAssembly) off the main thread for
// the personal workspace: OpenAPI import, design edits, lint, change
// detection, docs, mock responses and sending requests with scripts.
importScripts("wasm_exec.js");

const ready = new Promise((resolve, reject) => {
  self.addEventListener("requara-core-ready", () => resolve(), { once: true });
  const go = new Go();
  WebAssembly.instantiateStreaming(fetch("requara-core.wasm"), go.importObject)
    .then(({ instance }) => go.run(instance))
    .catch(reject);
});

self.onmessage = async (e) => {
  const { id, op, input } = e.data;
  try {
    await ready;
    const out = await self.requaraCore.call(op, JSON.stringify(input));
    self.postMessage({ id, ok: JSON.parse(out) });
  } catch (err) {
    self.postMessage({ id, err: { status: err?.status ?? 500, message: err?.message ?? String(err) } });
  }
};
