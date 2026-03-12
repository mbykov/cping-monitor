package main

import (
    "flag"
    "fmt"
    "log"
    "os"
    // "time"

    tea "github.com/charmbracelet/bubbletea"
    "cping/lib"
)

func setupLogging() {
    os.MkdirAll("logs", 0755)
    logFile := "logs/cping.log"

    f, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error opening log file: %v\n", err)
        return
    }
    log.SetOutput(f)
    log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
    log.Println("=== cping started ===")
}

func main() {
    setupLogging()

    configPath := flag.String("config", "config.yaml", "path to config file")
    flag.Parse()

    cfg, err := lib.LoadConfig(*configPath)
    if err != nil {
        log.Printf("❌ Error loading config: %v", err)
        fmt.Fprintf(os.Stderr, "❌ Error loading config: %v\n", err)
        os.Exit(1)
    }

    log.Printf("Config loaded: %+v", cfg)

    model := NewModel(cfg)
    p := tea.NewProgram(model, tea.WithAltScreen())
    model.SetProgram(p)

    if _, err := p.Run(); err != nil {
        log.Fatal(err)
    }
}
