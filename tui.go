package main

import (
    "fmt"
    "log"
    "strings"
    // "time"
    "runtime/debug"

    "cping/lib"

    "github.com/charmbracelet/bubbles/help"
    "github.com/charmbracelet/bubbles/key"
    "github.com/charmbracelet/bubbles/spinner"
    "github.com/charmbracelet/bubbles/table"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

// Сообщения от WebSocket
type wsConnectedMsg struct{}
type wsInterimMsg string
type wsFinalMsg string
type wsErrorMsg error

// Сообщения от рекордера
type recorderCreatedMsg struct {
    recorder *lib.Recorder
}
type recorderStartedMsg struct{}
type audioChunkMsg []float32
type recordingDoneMsg struct {
    data []byte
    size int
}
type recorderErrorMsg error

type keyMap struct {
    Start key.Binding
    Stop  key.Binding
    Quit  key.Binding
    Help  key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
    return []key.Binding{k.Start, k.Stop, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
    return [][]key.Binding{{k.Start, k.Stop, k.Quit}}
}

var keys = keyMap{
    Start: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start recording")),
    Stop:  key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "stop recording")),
    Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
    Help:  key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
}

type model struct {
    // UI компоненты
    spinner  spinner.Model
    viewport viewport.Model
    table    table.Model
    help     help.Model
    cfg      *lib.Config
    program  *tea.Program

    // Состояние
    wsClient    *lib.WSClient
    recorder    *lib.Recorder
    recording   bool
    lastText    string
    lastInterim string
    logs        []string
    status      string

    // Метрики
    interimCount int
    finalCount   int

    width, height int
    keys          keyMap
}

func NewModel(cfg *lib.Config) *model {
    s := spinner.New()
    s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
    s.Spinner = spinner.Dot

    vp := viewport.New(80, 10)
    vp.Style = lipgloss.NewStyle().
        BorderStyle(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("63"))

    columns := []table.Column{
        {Title: "Metric", Width: 15},
        {Title: "Value", Width: 20},
    }
    rows := []table.Row{
        {"Status", "Ready"},
        {"Interim", "0"},
        {"Final", "0"},
    }
    t := table.New(table.WithColumns(columns), table.WithRows(rows), table.WithHeight(5))

    wsClient := lib.NewWSClient(
        cfg.Server.Host, cfg.Server.Port,
        cfg.Server.Cert, cfg.Server.Key,
        cfg.Server.Cert != "" && cfg.Server.Key != "",
    )

    return &model{
        spinner:  s,
        viewport: vp,
        table:    t,
        help:     help.New(),
        cfg:      cfg,
        wsClient: wsClient,
        status:   "Ready",
        logs:     []string{},
        keys:     keys,
    }
}

func (m *model) SetProgram(p *tea.Program) {
    m.program = p
}

func (m model) Init() tea.Cmd {
    log.Println("Initializing model")
    return tea.Batch(
        m.spinner.Tick,
        connectToServer(m.wsClient, m.program),
    )
}

func connectToServer(wsClient *lib.WSClient, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Println("Connecting to server...")
        if err := wsClient.Connect(); err != nil {
            log.Printf("Connection failed: %v", err)
            return wsErrorMsg(err)
        }

        // Запускаем чтение сообщений в отдельной горутине
        go readWebSocket(wsClient, program)

        return wsConnectedMsg{}
    }
}

func readWebSocket(wsClient *lib.WSClient, program *tea.Program) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Recovered in readWebSocket: %v", r)
        }
    }()

    for {
        msgType, text, err := wsClient.ReadMessage()
        if err != nil {
            if program != nil {
                program.Send(wsErrorMsg(err))
            }
            return
        }

        switch msgType {
        case "interim":
            if program != nil {
                program.Send(wsInterimMsg(text))
            }
        case "final":
            if program != nil {
                program.Send(wsFinalMsg(text))
            }
        }
    }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height
        m.viewport.Width = msg.Width - 4
        m.viewport.Height = msg.Height - 15

    case tea.KeyMsg:
        switch {
        case key.Matches(msg, m.keys.Quit):
            return m, tea.Quit
        case key.Matches(msg, m.keys.Start):
            if !m.recording {
                m.status = "Creating recorder..."
                cmds = append(cmds, createRecorder(m.cfg.Audio.SampleRate, m.cfg.Audio.ChunkMs, m.program))
            }
        case key.Matches(msg, m.keys.Stop):
            if m.recording && m.recorder != nil {
                m.status = "Stopping..."
                cmds = append(cmds, stopRecording(m.recorder, m.program))
            }
        }

    case spinner.TickMsg:
        var cmd tea.Cmd
        m.spinner, cmd = m.spinner.Update(msg)
        cmds = append(cmds, cmd)

    // Сообщения от WebSocket
    case wsConnectedMsg:
        m.status = "Connected"
        cmds = append(cmds, addLog("✅ Connected to server"))

    case wsInterimMsg:
        m.lastInterim = string(msg)
        m.interimCount++
        m.updateTable()
        cmds = append(cmds, addLog(fmt.Sprintf("📝 Interim: %s", msg)))

    case wsFinalMsg:
        m.lastText = string(msg)
        m.finalCount++
        m.updateTable()
        cmds = append(cmds, addLog(fmt.Sprintf("🎯 Final: %s", msg)))

    case wsErrorMsg:
        cmds = append(cmds, addLog(fmt.Sprintf("❌ WS Error: %v", msg)))

    // Сообщения от рекордера
    case recorderCreatedMsg:
        m.recorder = msg.recorder
        m.recording = true
        m.status = "Starting..."
        cmds = append(cmds, startRecording(msg.recorder, m.program))

    case recorderStartedMsg:
        m.status = "Recording..."
        cmds = append(cmds, addLog("▶️ Recording started"))

    case audioChunkMsg:
        // Отправляем чанк на сервер
        if m.wsClient != nil {
            go func(chunk []float32) {
                if err := m.wsClient.SendAudioFloat32(chunk); err != nil {
                    log.Printf("Send error: %v", err)
                }
            }(msg)
        }

    case recordingDoneMsg:
        m.recording = false
        m.status = fmt.Sprintf("Saved %d bytes", msg.size)
        cmds = append(cmds, addLog(fmt.Sprintf("💾 Recording saved (%d bytes)", msg.size)))

    case recorderErrorMsg:
        m.recording = false
        m.status = "Error"
        cmds = append(cmds, addLog(fmt.Sprintf("❌ Recorder error: %v", msg)))
    }

    return m, tea.Batch(cmds...)
}

func (m *model) updateTable() {
    rows := []table.Row{
        {"Status", m.status},
        {"Interim", fmt.Sprintf("%d", m.interimCount)},
        {"Final", fmt.Sprintf("%d", m.finalCount)},
    }
    m.table.SetRows(rows)
}

func (m model) View() string {
    var b strings.Builder
    b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).Render("🎤 cping") + "\n\n")
    b.WriteString(m.table.View() + "\n\n")

    if m.recording {
        b.WriteString(fmt.Sprintf("%s %s\n\n", m.spinner.View(), m.status))
    } else {
        b.WriteString(m.status + "\n\n")
    }

    if m.lastInterim != "" {
        b.WriteString(fmt.Sprintf("Interim: %s\n\n", m.lastInterim))
    }

    b.WriteString(m.viewport.View() + "\n")
    b.WriteString(m.help.View(m.keys))
    return b.String()
}

// Команды
func createRecorder(sampleRate, chunkMs int, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        defer func() {
            if r := recover(); r != nil {
                log.Printf("🔥🔥🔥 PANIC in createRecorder: %v", r)
                debug.PrintStack()
            }
        }()

        log.Println("Creating recorder...")
        recorder, err := lib.NewRecorder(sampleRate, chunkMs)
        if err != nil {
            return recorderErrorMsg(err)
        }

        // Запускаем чтение аудио в отдельной горутине
        go readAudio(recorder, program)

        return recorderCreatedMsg{recorder: recorder}
    }
}

func startRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Println("Starting recording...")
        if err := recorder.StartRecording(); err != nil {
            return recorderErrorMsg(err)
        }
        return recorderStartedMsg{}
    }
}

func stopRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Println("Stopping recording...")
        data, err := recorder.StopRecording()
        if err != nil {
            return recorderErrorMsg(err)
        }
        return recordingDoneMsg{
            data: data,
            size: len(data),
        }
    }
}

func readAudio(recorder *lib.Recorder, program *tea.Program) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Recovered in readAudio: %v", r)
        }
    }()

    chunkChan := recorder.GetChunkChan()
    for chunk := range chunkChan {
        if program != nil {
            program.Send(audioChunkMsg(chunk))
        }
    }
}

func addLog(msg string) tea.Cmd {
    return func() tea.Msg {
        return nil // TODO: добавить логирование
    }
}
