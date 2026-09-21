package tools

import (
	"github.com/TheTitanrain/w32"
	"sync"
)

var mcpConfirmMu sync.Mutex

func confirmMCP(name, args string) bool {
	mcpConfirmMu.Lock()
	defer mcpConfirmMu.Unlock()
	// MB_DEFBUTTON2 defaults to No; MB_TOPMOST prevents an invisible approval.
	const flags = 0x00000004 | 0x00000100 | 0x00040000 | 0x00000020
	result := w32.MessageBox(w32.HWND(0), "Permitir esta ferramenta no seu PC?\n\n"+name+"\n\n"+args, "Ollama DZ23 — Desktop Commander", flags)
	return result == w32.IDYES
}
