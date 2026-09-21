# Desktop Commander in Ollama DZ23

The desktop can connect to a configured MCP server through a local stdio client.
For **Remote Desktop Commander**, use the official OAuth endpoint through the
pinned `mcp-remote` client. This does not import a ChatGPT session or its tokens.

Requirements: Node.js, `mcp-remote@0.14.3`, and your Desktop Commander account
with a paired online device. The desktop must be built from the MCP feature
branch; adding this file to an older executable does not enable MCP.

Install the client into a user-owned directory:

```powershell
npm install --prefix "$env:LOCALAPPDATA\Ollama DZ23\mcp" --save-exact mcp-remote@0.14.3
```

Create `%APPDATA%\Ollama DZ23\mcp.json` with absolute paths for your installation:

```json
{
  "enabled": true,
  "command": "C:\\Program Files\\nodejs\\node.exe",
  "args": [
    "C:\\Users\\YOUR_USER\\AppData\\Local\\Ollama DZ23\\mcp\\node_modules\\mcp-remote\\dist\\proxy.js",
    "https://mcp.desktopcommander.app/mcp"
  ],
  "allowed_tools": [
    "list_devices", "list_directory", "read_file", "write_file", "edit_block",
    "start_process", "read_process_output", "interact_with_process",
    "start_search", "get_more_search_results", "stop_search"
  ]
}
```

Complete OAuth in the browser when the client requests it. Tool names must match
those returned by the server. Keys and OAuth tokens stay outside the repository.
The child client receives a restricted operating-system environment, excluding
model API keys and other application credentials.

Select a model with verified tool-call support, enable **Acessar PC** next to the
message controls, and ask it to list a test directory. Tool access is opt-in for
the conversation form; no tool executes without a native confirmation showing
its name and full arguments. Rejecting a call returns a refusal to the model.
The Windows confirmation defaults to No. Requests are bounded to 24 calls,
12 model rounds and 150 seconds per tool call. Results are text-only and bounded;
image/audio tool results are not rendered by this first integration.

The configuration is loaded on each opted-in message. Set `enabled` to false or
turn off **Acessar PC** to disconnect. A missing configuration or failed OAuth
connection produces an error instead of pretending that a tool executed.

Remote tool outputs are untrusted data. Review operations that delete data,
change access permissions, deploy software or involve credentials. The remote
service's authorization and device controls remain authoritative.
