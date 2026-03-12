// github.com /gordonklaus/portaudio [1]

package main

import (
    "log"
    "github.com/gordonklaus/portaudio"
)

func main() {
    log.Println("Initializing PortAudio...")
    err := portaudio.Initialize()
    if err != nil {
        log.Fatalf("Initialize failed: %v", err)
    }
    defer portaudio.Terminate()

    // ВМЕСТО Devices() вызываем только Default
    log.Println("Getting default device...")
    defaultInput, err := portaudio.DefaultInputDevice()
    if err != nil {
        log.Fatalf("❌ No default input device: %v", err)
    }

    log.Printf("✅ Success! Default input: %s (channels: %d)",
        defaultInput.Name, defaultInput.MaxInputChannels)
}
