package main

import (
    "fmt"
    "strings"
    "time"
    "encoding/json"

    "cping/lib"

    "github.com/charmbracelet/bubbles/table"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

// --- Типы сообщений ---
type wsConnectedMsg struct{}
type wsErrorMsg error
type logMsg string
type tickMsg time.Time

type recorderCreatedMsg struct{ recorder *lib.Recorder }
type recorderStartedMsg struct{}
type audioChunkMsg []float32
type recordingDoneMsg struct {
    data []byte
    size int
}
type recorderErrorMsg error

// --- Модель ---
type model struct {
    viewport    viewport.Model
    table       table.Model
    cfg         *lib.Config
    wsClient    *lib.WSClient
    recorder    *lib.Recorder
    program     *tea.Program
    recording   bool
    lastSamples []float32
    referenceText string
    lastInterim   string
    currentHypothesis string
    status      string
    cpu, ram    string
    logs        []string
    width, height int
    jsonHistory   []string      // История JSON ответов

    stats struct {
        interimCount  int
        finalCount    int
        correctCount  int
        commandCount  int
        lastResponse  time.Time
        lastWER       float64
        avgWER        float64
        testCount     int
    }
}

func NewModel(cfg *lib.Config) *model {
    vp := viewport.New(80, 10)
    vp.Style = lipgloss.NewStyle().
        BorderStyle(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("63"))

    t := table.New(
        table.WithColumns([]table.Column{
            {Title: "Metric", Width: 15},
            {Title: "Value", Width: 35},
        }),
        table.WithHeight(8),
    )

    wsClient := lib.NewWSClient(
        cfg.Server.Host, cfg.Server.Port,
        cfg.Server.Cert, cfg.Server.Key,
        cfg.Server.Cert != "",
    )

    return &model{
        viewport: vp,
        table:    t,
        cfg:      cfg,
        wsClient: wsClient,
        status:   "Ready",
        jsonHistory:   make([]string, 0),
    }
}

func (m *model) SetProgram(p *tea.Program) { m.program = p }

func (m model) Init() tea.Cmd {
    return tea.Batch(
        connectToServer(m.wsClient, m.program),
        doTick(),
    )
}

func doTick() tea.Cmd {
    return tea.Tick(time.Second, func(t time.Time) tea.Msg {
        return tickMsg(t)
    })
}

// --- WER calculation ---
func calculateWER(reference, hypothesis string) float64 {
    if reference == "" {
        return 1.0
    }

    refWords := strings.Fields(reference)
    hypWords := strings.Fields(hypothesis)

    if len(refWords) == 0 {
        return 1.0
    }

    // Простейший WER: считаем несовпадающие слова
    differences := 0
    minLen := len(refWords)
    if len(hypWords) < minLen {
        minLen = len(hypWords)
    }

    for i := 0; i < minLen; i++ {
        if refWords[i] != hypWords[i] {
            differences++
        }
    }

    // Добавляем разницу в длине как ошибки
    differences += abs(len(refWords) - len(hypWords))

    return float64(differences) / float64(len(refWords))
}

func abs(x int) int {
    if x < 0 {
        return -x
    }
    return x
}

// --- Update ---
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    // 1. Системные и UI сообщения
    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height
        m.viewport.Width = msg.Width - 4
        m.viewport.Height = msg.Height - 18

    case tea.KeyMsg:
        switch msg.String() {
        case "q", "ctrl+c":
            return m, tea.Quit

        case "r":
            if !m.recording {
                // НАЧАЛО ЗАПИСИ: очищаем viewport и interim, НО НЕ referenceText
                m.lastInterim = ""
                m.currentHypothesis = ""
                m.viewport.SetContent("")
                m.status = "Recording..."
                return m, createRecorder(m.cfg.Audio.SampleRate, m.cfg.Audio.ChunkMs, m.program)
            }
            // КОНЕЦ ЗАПИСИ
            m.status = "Stopping..."
            return m, stopRecording(m.recorder, m.program)

        case "s":
            if len(m.lastSamples) > 0 {
                m.status = "Testing: Replaying..."
                m.currentHypothesis = ""
                m.wsClient.SendBuffer(m.lastSamples, m.cfg.Audio.ChunkMs, m.cfg.Audio.SampleRate)
                m.stats.testCount++
            }

        case "c":
            m.referenceText = ""
            m.lastInterim = ""
            m.currentHypothesis = ""
            m.jsonHistory = make([]string, 0)  // Очищаем историю
            m.viewport.SetContent("")  // Полностью очищаем
            // Сбрасываем статистику
            m.stats.interimCount = 0
            m.stats.finalCount = 0
            m.stats.correctCount = 0
            m.stats.commandCount = 0
            m.stats.lastWER = 0
            m.stats.avgWER = 0
            m.stats.testCount = 0
            m.status = "Cleared"
        }

    case tickMsg:
        m.cpu, m.ram = lib.GetSystemStats()
        m.updateTable()
        return m, doTick()

    // 2. Сообщения от WebSocket
    case map[string]interface{}:
        m.handleServerMsg(msg)

    case wsConnectedMsg:
        m.status = "Connected"
        cmds = append(cmds, addLog("✅ Connected to server"))

    case wsErrorMsg:
        m.status = "Disconnected"
        cmds = append(cmds, addLog(fmt.Sprintf("❌ Connection error: %v", msg)))

    // 3. Сообщения от Рекордера
    case audioChunkMsg:
        if m.recording && m.wsClient != nil {
            _ = m.wsClient.SendAudioFloat32([]float32(msg))
        }

    case recorderCreatedMsg:
        m.recorder = msg.recorder
        m.recording = true
        return m, startRecording(m.recorder, m.program)

    case recordingDoneMsg:
        m.recording = false
        if m.recorder != nil {
            m.lastSamples = m.recorder.GetAllSamples()
        }
        m.status = "Ready (Press 's' to replay)"

    case logMsg:
        m.logs = append(m.logs, string(msg))
        if len(m.logs) > 50 {
            m.logs = m.logs[1:]
        }
        m.viewport.SetContent(strings.Join(m.logs, "\n"))
        m.viewport.GotoBottom()
    }

    return m, tea.Batch(cmds...)
}

func (m *model) handleServerMsg(msg map[string]interface{}) {
    t := fmt.Sprint(msg["type"])

    // Собираем статистику
    switch t {
    case "interim":
        m.stats.interimCount++
        if text, ok := msg["text"].(string); ok {
            m.lastInterim = text
        }

    case "final":
        m.stats.finalCount++
        // Ничего не показываем, только статистика

    case "correct":
        m.stats.correctCount++
        if text, ok := msg["text"].(string); ok {
            // Добавляем текст в referenceText с пунктуацией
            if m.referenceText == "" {
                m.referenceText = text
            } else {
                // Добавляем пробел между предложениями, если нужно
                if !strings.HasSuffix(m.referenceText, " ") &&
                   !strings.HasSuffix(m.referenceText, ".") &&
                   !strings.HasSuffix(m.referenceText, "!") &&
                   !strings.HasSuffix(m.referenceText, "?") {
                    m.referenceText += " "
                }
                m.referenceText += text
            }
            m.currentHypothesis = text
            // Добавляем JSON в viewport (не заменяем, а добавляем)
            m.appendJSON(msg, "📝 CORRECTED TEXT")
        }

    case "command":
        m.stats.commandCount++
        if text, ok := msg["text"].(string); ok {
            m.currentHypothesis = text
            m.appendJSON(msg, "⚙️ COMMAND")
        }
    }

    m.stats.lastResponse = time.Now()

    // Если это ответ на тест (после нажатия 's'), считаем WER
    if m.stats.testCount > 0 && m.currentHypothesis != "" && m.referenceText != "" {
        wer := calculateWER(m.referenceText, m.currentHypothesis)
        m.stats.lastWER = wer
        // Обновляем средний WER
        if m.stats.testCount == 1 {
            m.stats.avgWER = wer
        } else {
            m.stats.avgWER = (m.stats.avgWER*float64(m.stats.testCount-1) + wer) / float64(m.stats.testCount)
        }
    }
}

func (m *model) appendJSON(msg map[string]interface{}, header string) {
    prettyJSON, _ := json.MarshalIndent(msg, "", "  ")

    // Формируем строку для этого сообщения
    msgStr := fmt.Sprintf("%s:\n%s", header, string(prettyJSON))

    // Добавляем в историю
    m.jsonHistory = append(m.jsonHistory, msgStr)

    // Ограничиваем историю (например, последние 10 сообщений)
    if len(m.jsonHistory) > 10 {
        m.jsonHistory = m.jsonHistory[1:]
    }

    // Обновляем viewport
    m.viewport.SetContent(strings.Join(m.jsonHistory, "\n\n"))

    // Прокручиваем вниз
    m.viewport.GotoBottom()
}


func (m *model) showJSON(msg map[string]interface{}, header string) {
    prettyJSON, _ := json.MarshalIndent(msg, "", "  ")
    m.viewport.SetContent(fmt.Sprintf("%s:\n%s", header, string(prettyJSON)))
    m.viewport.GotoTop()
}

func (m *model) updateTable() {
    rows := []table.Row{
        {"Status", m.status},
        {"CPU", m.cpu},
        {"RAM", m.ram},
        {"Interim", fmt.Sprintf("%d", m.stats.interimCount)},
        {"Final", fmt.Sprintf("%d", m.stats.finalCount)},
        {"Correct", fmt.Sprintf("%d", m.stats.correctCount)},
        {"Command", fmt.Sprintf("%d", m.stats.commandCount)},
        {"Tests", fmt.Sprintf("%d", m.stats.testCount)},
    }

    // Добавляем WER если есть
    if m.stats.lastWER > 0 {
        rows = append(rows,
            table.Row{"Last WER", fmt.Sprintf("%.1f%%", m.stats.lastWER*100)},
            table.Row{"Avg WER", fmt.Sprintf("%.1f%%", m.stats.avgWER*100)},
        )
    }

    if !m.stats.lastResponse.IsZero() {
        rows = append(rows, table.Row{"Last", m.stats.lastResponse.Format("15:04:05")})
    }

    m.table.SetRows(rows)
}

func (m model) View() string {
    var b strings.Builder

    // Заголовок
    title := lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("63")).
        Render("🎤 CPING MONITOR")
    b.WriteString(title + "\n\n")

    // Таблица статистики
    b.WriteString(m.table.View() + "\n\n")

    // Interim
    if m.lastInterim != "" {
        interimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
        b.WriteString(interimStyle.Render("Interim: " + m.lastInterim) + "\n")
    } else {
        b.WriteString("\n")
    }

    // REF с цветовой индикацией WER
    if m.referenceText != "" {
        var refStyle lipgloss.Style
        if m.stats.lastWER > 0.1 {
            refStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // красный
        } else if m.stats.lastWER > 0.05 {
            refStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // желтый
        } else {
            refStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // зеленый
        }
        b.WriteString(refStyle.Render("REF: "+m.referenceText) + "\n\n")
    } else {
        b.WriteString("\n")
    }

    // Viewport с ответами сервера
    b.WriteString(m.viewport.View() + "\n")

    // Подсказки
    helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
    b.WriteString(helpStyle.Render("r: record/stop | s: test | c: clear | q: quit"))

    return b.String()
}

// --- Команды и горутины ---

func connectToServer(wsClient *lib.WSClient, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        if err := wsClient.Connect(); err != nil {
            return wsErrorMsg(err)
        }
        go func() {
            for {
                msg, err := wsClient.ReadRawMessage()
                if err != nil {
                    if program != nil {
                        program.Send(wsErrorMsg(err))
                    }
                    return
                }
                if program != nil {
                    program.Send(msg)
                }
            }
        }()
        return wsConnectedMsg{}
    }
}

func createRecorder(sampleRate, chunkMs int, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        recorder, err := lib.NewRecorder(sampleRate, chunkMs)
        if err != nil {
            return recorderErrorMsg(err)
        }
        go func() {
            ch := recorder.GetChunkChan()
            for chunk := range ch {
                program.Send(audioChunkMsg(chunk))
            }
        }()
        return recorderCreatedMsg{recorder: recorder}
    }
}

func startRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        if err := recorder.StartRecording(); err != nil {
            return recorderErrorMsg(err)
        }
        return recorderStartedMsg{}
    }
}

func stopRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        data, err := recorder.StopRecording()
        if err != nil {
            return recorderErrorMsg(err)
        }
        return recordingDoneMsg{data: data, size: len(data)}
    }
}

func addLog(msg string) tea.Cmd {
    return func() tea.Msg { return logMsg(msg) }
}
