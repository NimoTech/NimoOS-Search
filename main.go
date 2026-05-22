package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/NimoTech/NimoOS-Search/common"
	v1 "github.com/NimoTech/NimoOS-Search/route/v1"
	"github.com/labstack/echo/v4"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-v" {
		fmt.Printf("v%s\n", common.Version)
		os.Exit(0)
	}
	e := echo.New()
	v1.NewRouter(e)
	if err := http.ListenAndServe("127.0.0.1:0", e); err != nil {
		fmt.Fprintf(os.Stderr, "nimoos-search: %v\n", err)
		os.Exit(1)
	}
}
