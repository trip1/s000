package functions

type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Content     string `json:"content"`
}

func BuiltinTemplates() []Template {
	return []Template{
		{
			Name:        "hello-json",
			Description: "Echoes input and allows request",
			Language:    "javascript",
			Content: `// Reads JSON from stdin and writes JSON to stdout.
const fs = require('fs');
const input = fs.readFileSync(0, 'utf8');
const event = JSON.parse(input || '{}');
process.stdout.write(JSON.stringify({ continue: true, output: event }));
`,
		},
		{
			Name:        "block-prefix",
			Description: "Blocks events where key starts with blocked/",
			Language:    "javascript",
			Content: `const fs = require('fs');
const input = fs.readFileSync(0, 'utf8');
const event = JSON.parse(input || '{}');
const key = event.key || '';
if (key.startsWith('blocked/')) {
  process.stdout.write(JSON.stringify({ continue: false, output: { reason: 'blocked prefix' } }));
} else {
  process.stdout.write(JSON.stringify({ continue: true }));
}
`,
		},
		{
			Name:        "http-audit",
			Description: "Annotates HTTP events",
			Language:    "javascript",
			Content: `const fs = require('fs');
const input = fs.readFileSync(0, 'utf8');
const event = JSON.parse(input || '{}');
process.stdout.write(JSON.stringify({ continue: true, output: { path: event.path, method: event.method } }));
`,
		},
	}
}
