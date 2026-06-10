#!/usr/bin/env node
/**
 * Demo 4: Cliente Node.js puro (sin dependencias externas).
 * Usa solo el módulo `http` built-in. Funciona en cualquier Node 18+.
 *
 * Si querés usar el SDK oficial de OpenAI, mirá `node_sdk_example.js`
 * (requiere `npm install openai`).
 *
 * Uso:
 *     node examples/node_client.js non-stream
 *     node examples/node_client.js stream
 */

const http = require("http");

const GATEWAY_HOST = "localhost";
const GATEWAY_PORT = 8080;
const GATEWAY_KEY = "demo-key-1234567890";  // Reemplazar con tu GATEWAY_API_KEY
const MODEL = "gemma3:1b";
const USER_MESSAGE = "¿Qué es un circuit breaker en 1 frase?";

function postJson(payload) {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify(payload);
    const req = http.request(
      {
        host: GATEWAY_HOST,
        port: GATEWAY_PORT,
        path: "/v1/chat/completions",
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Authorization": `Bearer ${GATEWAY_KEY}`,
          "Content-Length": Buffer.byteLength(body),
        },
      },
      (res) => {
        let chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          resolve({
            status: res.statusCode,
            headers: res.headers,
            body: Buffer.concat(chunks).toString("utf8"),
          });
        });
      }
    );
    req.on("error", reject);
    req.write(body);
    req.end();
  });
}

async function chatNonStreaming() {
  const start = Date.now();
  const resp = await postJson({
    model: MODEL,
    messages: [{ role: "user", content: USER_MESSAGE }],
  });
  const elapsed = Date.now() - start;
  console.log(`[non-stream] HTTP ${resp.status} in ${elapsed}ms`);
  console.log(`  X-Cache-Status: ${resp.headers["x-cache-status"]}`);
  console.log(`  X-Provider:     ${resp.headers["x-provider"]}`);
  console.log(`  X-Cache-Sim:    ${resp.headers["x-cache-similarity"]}`);
  const data = JSON.parse(resp.body);
  console.log(`  Response: ${data.choices[0].message.content}`);
}

function chatStreaming() {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify({
      model: MODEL,
      stream: true,
      messages: [{ role: "user", content: USER_MESSAGE }],
    });
    const start = Date.now();
    let firstChunkMs = null;
    let full = "";

    const req = http.request(
      {
        host: GATEWAY_HOST,
        port: GATEWAY_PORT,
        path: "/v1/chat/completions",
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Authorization": `Bearer ${GATEWAY_KEY}`,
          "Content-Length": Buffer.byteLength(body),
        },
      },
      (res) => {
        console.log(`[stream] HTTP ${res.statusCode}, Content-Type: ${res.headers["content-type"]}`);
        console.log(`  X-Cache-Status: ${res.headers["x-cache-status"]}`);
        console.log(`  X-Provider:     ${res.headers["x-provider"]}`);
        console.log(`  X-Cache-Sim:    ${res.headers["x-cache-similarity"]}`);
        console.log("\n--- Streaming output ---");

        let buf = "";
        res.on("data", (chunk) => {
          buf += chunk.toString("utf8");
          const lines = buf.split("\n");
          buf = lines.pop(); // última línea incompleta queda en buffer
          for (const line of lines) {
            if (line.startsWith("data: ")) {
              if (firstChunkMs === null) firstChunkMs = Date.now() - start;
              const payload = line.slice(6);
              if (payload === "[DONE]") {
                console.log("\n--- [DONE] ---");
                console.log(`\n[stream] First chunk: ${firstChunkMs}ms, total: ${Date.now() - start}ms`);
                console.log(`[stream] Full: ${full}`);
                resolve();
                return;
              }
              try {
                const j = JSON.parse(payload);
                const delta = j.choices[0].delta.content || "";
                full += delta;
                process.stdout.write(delta);
              } catch (e) {}
            }
          }
        });
        res.on("end", () => resolve());
        res.on("error", reject);
      }
    );
    req.on("error", reject);
    req.write(body);
    req.end();
  });
}

async function main() {
  const mode = process.argv[2] || "non-stream";
  if (mode === "non-stream") {
    await chatNonStreaming();
  } else if (mode === "stream") {
    await chatStreaming();
  } else {
    console.log(`Unknown mode: ${mode}. Use 'non-stream' or 'stream'.`);
    process.exit(1);
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
