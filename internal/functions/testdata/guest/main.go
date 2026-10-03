//go:build ignore

// Guest is the WASI test fixture for the functions runner. It is compiled to
// wasm at test time (GOOS=wasip1 GOARCH=wasm). Behavior is driven by env vars:
//
//	MODE=spin  -> loop forever (exercises the host timeout)
//	MODE=fail  -> write to stderr and exit non-zero
//	(default)  -> echo stdin + report what the sandbox exposes
//
// It also probes the sandbox: filesystem access must be denied and arbitrary
// host env (HOME/PATH) must not leak.
package main

import (
	"encoding/json"
	"io"
	"os"
)

func main() {
	switch os.Getenv("MODE") {
	case "spin":
		for i := 0; ; i++ {
			_ = i // busy loop; interrupted by the host when the deadline passes
		}
	case "fail":
		os.Stderr.WriteString("boom: intentional failure\n")
		os.Exit(3)
	}

	in, _ := io.ReadAll(os.Stdin)
	resp := map[string]any{
		"echo":     string(in),
		"greeting": os.Getenv("GREETING"),
		"subject":  os.Getenv("PS_PRINCIPAL_SUBJECT"),
		"method":   os.Getenv("PS_METHOD"),
	}
	if _, err := os.ReadDir("/"); err != nil {
		resp["fs"] = "denied"
	} else {
		resp["fs"] = "ALLOWED"
	}
	if os.Getenv("HOME") != "" || os.Getenv("PATH") != "" {
		resp["host_env"] = "LEAKED"
	} else {
		resp["host_env"] = "clean"
	}
	out, _ := json.Marshal(resp)
	os.Stdout.Write(out)
}
