package main

import (
    "flag"
    "fmt"
    "log"
    "os"

    "github.com/mbykov/cping/lib"
)

func main() {
    configPath := flag.String("config", "config.yaml", "path to config file")
    flag.Parse()

    cfg, err := lib.LoadConfig(*configPath)
    if err != nil {
        fmt.Printf("❌ Error loading config: %v\n", err)
        os.Exit(1)
    }

    fmt.Printf("✅ Config loaded: %+v\n", cfg)
}
