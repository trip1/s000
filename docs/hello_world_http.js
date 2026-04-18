const decoder = new TextDecoder();
const encoder = new TextEncoder();

function readAllStdin() {
  const chunks = [];
  let total = 0;
  while (true) {
    const buf = new Uint8Array(1024);
    const n = Javy.IO.readSync(0, buf);
    if (!n || n <= 0) {
      break;
    }
    chunks.push(buf.subarray(0, n));
    total += n;
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.length;
  }
  return out;
}

function readInputJSON() {
  const bytes = readAllStdin();
  if (!bytes || bytes.length === 0) {
    return {};
  }
  try {
    return JSON.parse(decoder.decode(bytes));
  } catch (_err) {
    return {};
  }
}

const event = readInputJSON();

const result = {
  continue: true,
  output: {
    status: 200,
    headers: {
      "content-type": "application/json"
    },
    body: {
      message: "hello world",
      method: event.method || "GET",
      path: event.path || "/"
    }
  }
};

Javy.IO.writeSync(1, encoder.encode(JSON.stringify(result)));
