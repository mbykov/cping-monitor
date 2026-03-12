package lib

import (
    "fmt"
    "log"
    "os"
    // "time"

    "github.com/gordonklaus/portaudio"
)

type Recorder struct {
    sampleRate int
    chunkSize  int
    stream     *portaudio.Stream
    buffer     []int16
    recording  bool
    chunkChan  chan []float32
    allSamples []float32 // Накопительный буфер для всей сессии записи
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

    log.Println("Getting default input device...")
    device, err := portaudio.DefaultInputDevice()
    if err != nil {
        portaudio.Terminate()
        return nil, fmt.Errorf("no default input device: %w", err)
    }
    log.Printf("✅ Using device: %s (channels: %d)", device.Name, device.MaxInputChannels)

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

func (r *Recorder) processAudio(in [][]int16, out [][]int16, timeInfo portaudio.StreamCallbackTimeInfo, flags portaudio.StreamCallbackFlags) {
    if !r.recording || len(in) == 0 || len(in[0]) == 0 {
        return
    }

    floatChunk := make([]float32, len(in[0]))
    for i, sample := range in[0] {
        f := float32(sample) / 32768.0
        floatChunk[i] = f
    }

    // Сохраняем в общий буфер для последующего повтора (кнопка 's')
    r.allSamples = append(r.allSamples, floatChunk...)

    select {
    case r.chunkChan <- floatChunk:
    default:
        // Канал полон, пропускаем для UI, но в allSamples данные уже сохранились
    }
}

func (r *Recorder) GetAllSamples() []float32 {
    return r.allSamples
}

func (r *Recorder) StartRecording() error {
    log.Println("Starting recording stream...")
    r.buffer = make([]int16, 0)
    r.recording = true
    return r.stream.Start()
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
    header[17] = 0
    header[18] = 0
    header[19] = 0
    header[20] = 1
    header[21] = 0
    header[22] = 1
    header[23] = 0
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
    header[33] = 0
    header[34] = 16
    header[35] = 0

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
