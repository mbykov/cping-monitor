package lib

import (
    "fmt"
    "log"
    "os"
    // "runtime/debug"

    "github.com/gordonklaus/portaudio"
)

type Recorder struct {
    sampleRate int
    chunkSize  int
    stream     *portaudio.Stream
    buffer     []int16
    recording  bool
    chunkChan  chan []float32
}

func NewRecorder(sampleRate, chunkMs int) (*Recorder, error) {
    log.Println("=== START NewRecorder ===")
    log.Printf("sampleRate=%d, chunkMs=%d", sampleRate, chunkMs)

    log.Println("Initializing PortAudio...")

    err := portaudio.Initialize()
    if err != nil {
        return nil, fmt.Errorf("portaudio init failed: %w", err)
    }
    log.Println("✅ PortAudio initialized")

    // Получаем список всех устройств с защитой от паники
    log.Println("Getting device list...")

    // var devices []*portaudio.DeviceInfo
    // var devErr error

    // func() {
    //     defer func() {
    //         if r := recover(); r != nil {
    //             log.Printf("🔥🔥🔥 PANIC in portaudio.Devices(): %v", r)
    //             debug.PrintStack()
    //             devErr = fmt.Errorf("panic: %v", r)
    //         }
    //     }()
    //     devices, devErr = portaudio.Devices()
    // }()

    // if devErr != nil {
    //     portaudio.Terminate()
    //     return nil, fmt.Errorf("can't get devices: %w", devErr)
    // }

    // Ищем первое устройство с входом (микрофон)
    // var device *portaudio.DeviceInfo
    // for i, d := range devices {
    //     log.Printf("Device %d: %s (inputs: %d, outputs: %d)",
    //         i, d.Name, d.MaxInputChannels, d.MaxOutputChannels)
    //     if d.MaxInputChannels > 0 {
    //         device = d
    //         log.Printf("✅ Found input device: %s", d.Name)
    //         break
    //     }
    // }

    // if device == nil {
    //     portaudio.Terminate()
    //     return nil, fmt.Errorf("no input device found")
    // }
    log.Println("Getting default binput device...")
    device, err := portaudio.DefaultInputDevice()
    if err != nil {
        portaudio.Terminate()
        return nil, fmt.Errorf("no default input device: %w", err)
    }
    log.Printf("✅ Using device: %s", device.Name)


    chunkSize := sampleRate * chunkMs / 1000
    log.Printf("Chunk size: %d frames", chunkSize)

    r := &Recorder{
        sampleRate: sampleRate,
        chunkSize:  chunkSize,
        buffer:     make([]int16, 0),
        chunkChan:  make(chan []float32, 100),
    }

    log.Println("Creating stream parameters...")
    param := portaudio.LowLatencyParameters(device, nil)
    param.Input.Channels = 1
    param.SampleRate = float64(sampleRate)
    param.FramesPerBuffer = chunkSize

    log.Println("Opening stream...")
    stream, err := portaudio.OpenStream(param, r.processAudio)
    if err != nil {
        portaudio.Terminate()
        return nil, fmt.Errorf("open stream failed: %w", err)
    }
    r.stream = stream

    log.Println("✅ Recorder created successfully")
    return r, nil
}

// processAudio - правильная сигнатура для PortAudio
func (r *Recorder) processAudio(in [][]int16, out [][]int16, timeInfo portaudio.StreamCallbackTimeInfo, flags portaudio.StreamCallbackFlags) {
    log.Printf("processAudio called, recording=%v, in len=%d", r.recording, len(in))
    if !r.recording || len(in) == 0 || len(in[0]) == 0 {
        return
    }

    log.Printf("Got %d samples", len(in[0]))
    r.buffer = append(r.buffer, in[0]...)

    floatChunk := make([]float32, len(in[0]))
    for i, sample := range in[0] {
        floatChunk[i] = float32(sample) / 32768.0
    }

    select {
    case r.chunkChan <- floatChunk:
        log.Printf("Sent chunk of %d samples", len(floatChunk))
    default:
        log.Println("Chunk channel full, dropping chunk")
    }
}

func (r *Recorder) StartRecording() error {
    log.Println("Starting recording stream...")
    r.buffer = make([]int16, 0)
    r.recording = true
    err := r.stream.Start()
    log.Printf("Stream start result: %v", err)
    return err
}

func (r *Recorder) StopRecording() ([]byte, error) {
    log.Println("Stopping recording stream...")
    r.recording = false
    err := r.stream.Stop()
    if err != nil {
        return nil, err
    }
    close(r.chunkChan)
    return int16ToBytes(r.buffer), nil
}

func (r *Recorder) GetChunkChan() <-chan []float32 {
    return r.chunkChan
}

func (r *Recorder) Close() error {
    log.Println("Closing recorder...")
    err := r.stream.Close()
    portaudio.Terminate()
    return err
}

func int16ToBytes(samples []int16) []byte {
    b := make([]byte, len(samples)*2)
    for i, s := range samples {
        b[i*2] = byte(s)
        b[i*2+1] = byte(s >> 8)
    }
    return b
}

func SaveAudio(filename string, data []byte, sampleRate int) error {
    f, err := os.Create(filename)
    if err != nil {
        return err
    }
    defer f.Close()

    // WAV header
    header := make([]byte, 44)
    copy(header[0:4], []byte("RIFF"))
    fileSize := 36 + len(data)
    header[4] = byte(fileSize)
    header[5] = byte(fileSize >> 8)
    header[6] = byte(fileSize >> 16)
    header[7] = byte(fileSize >> 24)
    copy(header[8:12], []byte("WAVE"))

    copy(header[12:16], []byte("fmt "))
    header[16] = 16
    header[20] = 1
    header[22] = 1
    header[24] = byte(sampleRate)
    header[25] = byte(sampleRate >> 8)
    header[26] = byte(sampleRate >> 16)
    header[27] = byte(sampleRate >> 24)
    byteRate := sampleRate * 2
    header[28] = byte(byteRate)
    header[29] = byte(byteRate >> 8)
    header[30] = byte(byteRate >> 16)
    header[31] = byte(byteRate >> 24)
    header[32] = 2
    header[34] = 16

    copy(header[36:40], []byte("data"))
    header[40] = byte(len(data))
    header[41] = byte(len(data) >> 8)
    header[42] = byte(len(data) >> 16)
    header[43] = byte(len(data) >> 24)

    if _, err := f.Write(header); err != nil {
        return err
    }
    _, err = f.Write(data)
    return err
}
