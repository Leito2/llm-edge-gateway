/**
 * Demo 4b: Cliente Node.js usando el SDK oficial de OpenAI.
 *
 * Instalación:
 *     npm install openai
 *
 * Uso:
 *     GATEWAY_API_KEY=demo-key-1234567890 node examples/node_sdk_example.js non-stream
 *     GATEWAY_API_KEY=demo-key-1234567890 node examples/node_sdk_example.js stream
 *
 * Este ejemplo muestra que el gateway es 100% drop-in compatible con
 * el SDK de OpenAI: solo cambias baseURL y apiKey.
 */

const OpenAI = require("openai").default;

const client = new OpenAI({
  baseURL: "http://localhost:8080/v1",  // Apunta al gateway en vez de api.openai.com
  apiKey: process.env.GATEWAY_API_KEY || "demo-key-1234567890",
});

const MODEL = "gemma3:1b";
const USER_MESSAGE = "¿Qué es un circuit breaker en 1 frase?";

async function nonStream() {
  console.log("--- Non-streaming ---");
  const start = Date.now();
  const resp = await client.chat.completions.create({
    model: MODEL,
    messages: [{ role: "user", content: USER_MESSAGE }],
  });
  const elapsed = Date.now() - start;
  console.log(`[non-stream] ${elapsed}ms`);
  console.log(`  Response: ${resp.choices[0].message.content}`);
}

async function stream() {
  console.log("--- Streaming ---");
  const start = Date.now();
  let firstChunkMs = null;
  const s = await client.chat.completions.create({
    model: MODEL,
    messages: [{ role: "user", content: USER_MESSAGE }],
    stream: true,
  });
  for await (const chunk of s) {
    if (firstChunkMs === null) firstChunkMs = Date.now() - start;
    const delta = chunk.choices[0]?.delta?.content || "";
    process.stdout.write(delta);
  }
  console.log(`\n[stream] First chunk: ${firstChunkMs}ms, total: ${Date.now() - start}ms`);
}

async function main() {
  const mode = process.argv[2] || "non-stream";
  if (mode === "non-stream") await nonStream();
  else if (mode === "stream") await stream();
  else {
    console.log("Use: node examples/node_sdk_example.js [non-stream|stream]");
    process.exit(1);
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
