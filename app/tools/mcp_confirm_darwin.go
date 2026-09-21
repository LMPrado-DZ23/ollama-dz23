package tools

import (
	"github.com/ollama/ollama/app/dialog"
	"sync"
)

var mcpConfirmMu sync.Mutex

func confirmMCP(name, args string) bool {
	mcpConfirmMu.Lock()
	defer mcpConfirmMu.Unlock()
	return dialog.Message("Permitir ferramenta %s?\n\n%s", name, args).Title("Ollama DZ23 — Desktop Commander").YesNo()
}
