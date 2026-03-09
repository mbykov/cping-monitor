package main

import (
    "log"
    "github.com/gordonklaus/portaudio"
)

func main() {
    log.Println("Testing PortAudio...")

    err := portaudio.Initialize()
    if err != nil {
        log.Fatalf("Initialize failed: %v", err)
    }
    log.Println("Initialize OK")

    portaudio.Terminate()
    log.Println("Terminate OK")
}
