package main

import (
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"

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
type testAfterStopMsg struct{}

type recorderCreatedMsg struct{ recorder *lib.Recorder }
type recorderStartedMsg struct{}
type audioChunkMsg []float32
type recordingDoneMsg struct {
    data []byte
    size int
}
type recorderErrorMsg error

// Структура для результата теста
type testResult struct {
    number     int
    duration   time.Duration
    hypothesis string
    wer        float64
    timestamp  time.Time
}

// --- Модель ---
type model struct {
    leftPanel   viewport.Model // JSON панель (все ответы сервера)
    rightPanel  viewport.Model // TESTS панель (REF и результаты тестов)
    table       table.Model
    cfg         *lib.Config
    wsClient    *lib.WSClient
    recorder    *lib.Recorder
    program     *tea.Program
    recording   bool
    lastSamples []float32

    refText     string       // Эталонный текст (объединение всех фраз записи)
    tests       []testResult // История тестов
    currentHypothesis string // Накопленная гипотеза для текущего теста

    leftContent string // Сохраняем левую панель (все JSON ответы)

    testStart time.Time // Время начала текущего теста

    status      string
    cpu, ram    string
    logs        []string
    width, height int

    stats struct {
        interimCount int
        finalCount   int
        correctCount int
        commandCount int
        lastResponse time.Time
        lastWER      float64
        avgWER       float64
        testCount    int
    }
}

func newModel(cfg *lib.Config) *model {
    leftVP := viewport.New(40, 20)
    leftVP.Style = lipgloss.NewStyle().
        BorderStyle(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("63"))

    rightVP := viewport.New(40, 20)
    rightVP.Style = lipgloss.NewStyle().
        BorderStyle(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("63"))

    t := table.New(
        table.WithColumns([]table.Column{
            {Title: "Metric", Width: 15},
            {Title: "Value", Width: 25},
        }),
        table.WithHeight(5),
    )

    wsClient := lib.NewWSClient(
        cfg.Server.Host, cfg.Server.Port,
        cfg.Server.Cert, cfg.Server.Key,
        cfg.Server.Cert != "",
    )

    return &model{
        leftPanel:  leftVP,
        rightPanel: rightVP,
        table:      t,
        cfg:        cfg,
        wsClient:   wsClient,
        status:     "Ready",
        tests:      make([]testResult, 0),
        currentHypothesis: "",
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

// Обрезка строк для отображения
func truncateString(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen-1] + "…"
}

// Объединение предложений с правильной пунктуацией
func joinSentences(existing, new string) string {
    if existing == "" {
        return new
    }

    // Если existing уже заканчивается на знак препинания, добавляем пробел
    lastChar := existing[len(existing)-1:]
    if lastChar == "." || lastChar == "!" || lastChar == "?" || lastChar == " " {
        return existing + " " + new
    }

    // Иначе добавляем точку и пробел
    return existing + ". " + new
}

// Завершение текущего теста
func (m *model) finalizeTest() {
    if m.stats.testCount == 0 || m.currentHypothesis == "" {
        return
    }

    // Вычисляем WER
    wer := calculateWER(m.refText, m.currentHypothesis)
    m.stats.lastWER = wer

    // Вычисляем время
    duration := time.Since(m.testStart)

    // Создаем результат теста
    result := testResult{
        number:     m.stats.testCount,
        duration:   duration,
        hypothesis: m.currentHypothesis,
        wer:        wer,
        timestamp:  time.Now(),
    }

    // Добавляем в историю
    m.tests = append(m.tests, result)
    log.Printf("Тест #%d завершен: WER=%.2f%%, время=%.2fs, гипотеза=%s",
        result.number, wer*100, duration.Seconds(), m.currentHypothesis)

    // Обновляем правую панель
    m.updateRightPanel()

    // Сбрасываем текущую гипотезу
    m.currentHypothesis = ""
}

// --- Update ---
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    // 1. Системные и UI сообщения
    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height
        // Делим экран пополам для двух viewport
        panelWidth := (msg.Width - 10) / 2
        if panelWidth < 30 {
            panelWidth = 30
        }
        m.leftPanel.Width = panelWidth
        m.leftPanel.Height = msg.Height - 12
        m.rightPanel.Width = panelWidth
        m.rightPanel.Height = msg.Height - 12

    case tea.KeyMsg:
        switch msg.String() {
        case "q", "ctrl+c":
            if m.recorder != nil {
                m.recorder.Close()
            }
            return m, tea.Quit

        case "r":
            if !m.recording {
                // СТАРТ ЗАПИСИ - очищаем всё для новой сессии
                m.refText = ""
                m.tests = make([]testResult, 0)
                m.leftContent = ""
                m.currentHypothesis = ""
                m.leftPanel.SetContent("")
                m.rightPanel.SetContent("")
                m.stats.interimCount = 0
                m.stats.finalCount = 0
                m.stats.correctCount = 0
                m.stats.commandCount = 0
                m.stats.lastWER = 0
                m.stats.avgWER = 0
                m.stats.testCount = 0
                m.status = "Recording..."
                log.Println("Starting recording...")
                return m, createRecorder(m.cfg.Audio.SampleRate, m.cfg.Audio.ChunkMs, m.program)
            } else {
                // СТОП ЗАПИСИ
                m.status = "Stopping..."
                log.Println("Stopping recording...")
                return m, stopRecording(m.recorder, m.program)
            }

        case "s":
            // Если запись активна - сначала останавливаем
            if m.recording && m.recorder != nil {
                m.status = "Stopping..."
                log.Println("Stopping recording before test...")
                return m, tea.Sequence(
                    stopRecording(m.recorder, m.program),
                    func() tea.Msg { return testAfterStopMsg{} },
                )
            }

            // Если записи нет, но есть samples - запускаем тест
            if len(m.lastSamples) > 0 {
                m.testStart = time.Now()
                m.status = "Testing..."
                m.stats.testCount++
                m.currentHypothesis = "" // Очищаем гипотезу для нового теста
                log.Printf("Starting test #%d with %d samples", m.stats.testCount, len(m.lastSamples))
                m.wsClient.SendBuffer(m.lastSamples, m.cfg.Audio.ChunkMs, m.cfg.Audio.SampleRate)
                return m, nil
            }

        case "c":
            // ПОЛНАЯ ОЧИСТКА
            m.refText = ""
            m.tests = make([]testResult, 0)
            m.leftContent = ""
            m.currentHypothesis = ""
            m.leftPanel.SetContent("")
            m.rightPanel.SetContent("")
            m.stats.interimCount = 0
            m.stats.finalCount = 0
            m.stats.correctCount = 0
            m.stats.commandCount = 0
            m.stats.lastWER = 0
            m.stats.avgWER = 0
            m.stats.testCount = 0
            m.status = "Cleared"
            log.Println("Cleared all")
        }

    case tickMsg:
        m.cpu, m.ram = lib.GetSystemStats()
        m.updateTable()

        // Проверяем, не завис ли тест (больше 10 секунд прошло)
        if m.stats.testCount > 0 && m.currentHypothesis != "" && time.Since(m.testStart) > 10*time.Second {
            log.Printf("Тест #%d завершен по таймауту", m.stats.testCount)
            m.finalizeTest()
        }

        return m, doTick()

    // 2. Сообщения от WebSocket
    case map[string]interface{}:
        m.handleServerMsg(msg)

    case wsConnectedMsg:
        m.status = "Connected"
        log.Println("✅ Connected to server")

    case wsErrorMsg:
        m.status = "Disconnected"
        log.Printf("❌ Connection error: %v", msg)

    // 3. Сообщения от Рекордера
    case audioChunkMsg:
        if m.recording && m.wsClient != nil {
            err := m.wsClient.SendAudioFloat32([]float32(msg))
            if err != nil {
                log.Printf("Error sending audio: %v", err)
            }
        }

    case recorderCreatedMsg:
        m.recorder = msg.recorder
        m.recording = true
        log.Println("Recorder created, starting recording...")
        return m, startRecording(m.recorder, m.program)

    case recorderStartedMsg:
        log.Println("Recording started")
        m.status = "Recording... (speak now)"

    case recordingDoneMsg:
        m.recording = false
        if m.recorder != nil {
            m.lastSamples = m.recorder.GetAllSamples()
            log.Printf("Recording done, %d samples captured", len(m.lastSamples))
        }
        m.status = "Ready (Press 's' to test)"

    case testAfterStopMsg:
        // После остановки записи запускаем тест
        if len(m.lastSamples) > 0 {
            m.testStart = time.Now()
            m.status = "Testing..."
            m.stats.testCount++
            m.currentHypothesis = "" // Очищаем гипотезу для нового теста
            log.Printf("Starting test #%d after stop", m.stats.testCount)
            m.wsClient.SendBuffer(m.lastSamples, m.cfg.Audio.ChunkMs, m.cfg.Audio.SampleRate)
        }
        return m, nil

    case recorderErrorMsg:
        log.Printf("Recorder error: %v", msg)
        m.status = fmt.Sprintf("Error: %v", msg)
    }

    return m, tea.Batch(cmds...)
}

func (m *model) handleServerMsg(msg map[string]interface{}) {
    t := fmt.Sprint(msg["type"])
    log.Printf("Received message type: %s, recording=%v, testCount=%d", t, m.recording, m.stats.testCount)

    switch t {
    case "interim":
        m.stats.interimCount++
        if text, ok := msg["text"].(string); ok {
            log.Printf("Interim: %s", text)
        }

    case "final":
        m.stats.finalCount++
        log.Printf("Final message received")

    case "correct", "command":
        if t == "correct" {
            m.stats.correctCount++
        } else {
            m.stats.commandCount++
        }

        // Получаем текст
        text, hasText := msg["text"].(string)
        if !hasText {
            text = ""
        }
        log.Printf("%s text: %s", t, text)

        // Формируем JSON строку
        prettyJSON, _ := json.MarshalIndent(msg, "", "  ")
        jsonStr := string(prettyJSON)

        // ЕСЛИ МЫ НЕ В ТЕСТЕ (testCount == 0) - это запись
        if m.stats.testCount == 0 {
            // Добавляем JSON только для записи
            if m.leftContent == "" {
                m.leftContent = jsonStr
            } else {
                m.leftContent = m.leftContent + "\n\n" + jsonStr
            }
            m.leftPanel.SetContent(m.leftContent)
            m.leftPanel.GotoBottom()

            // Формируем REF из всех предложений
            if m.refText == "" {
                m.refText = text
                log.Printf("REF начат: %s", text)
            } else {
                m.refText = joinSentences(m.refText, text)
                log.Printf("REF обновлен: %s", m.refText)
            }

            // Обновляем правую панель с новым REF
            m.updateRightPanel()
        } else {
            // ЭТО ТЕСТ (testCount > 0) - накапливаем гипотезу, НО НЕ добавляем JSON
            if m.currentHypothesis == "" {
                m.currentHypothesis = text
            } else {
                m.currentHypothesis = joinSentences(m.currentHypothesis, text)
            }
            log.Printf("Накопленная гипотеза для теста #%d: %s", m.stats.testCount, m.currentHypothesis)

            // Обновляем правую панель чтобы показать процесс
            m.updateRightPanel()
        }
    }

    m.stats.lastResponse = time.Now()
}

func (m *model) updateRightPanel() {
    var rightContent strings.Builder

    // REF (всегда первый, одна строка)
    if m.refText != "" {
        refStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15")) // белый
        truncatedRef := truncateString(m.refText, 60)
        rightContent.WriteString(refStyle.Render("REF: " + truncatedRef) + "\n\n")
    }

    // Тесты - каждый тест это одна строка с результатом
    for i, test := range m.tests {
        // Выбираем цвет для номера
        var numStyle lipgloss.Style
        if test.wer > 0.1 {
            numStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // красный
        } else if test.wer > 0.05 {
            numStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // желтый
        } else {
            numStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // зеленый
        }

        // Формируем строку теста
        testLine := fmt.Sprintf("#%d (WER %.1f%%, %.2fs)",
            test.number, test.wer*100, test.duration.Seconds())
        rightContent.WriteString(numStyle.Render(testLine) + "\n")

        // Текст гипотезы (обрезанный)
        truncatedHyp := truncateString(test.hypothesis, 55)
        rightContent.WriteString("  " + truncatedHyp + "\n")

        // Добавляем разделитель между тестами, кроме последнего
        if i < len(m.tests)-1 {
            rightContent.WriteString("\n")
        }
    }

    // Если тест в процессе, показываем текущую гипотезу
    if m.stats.testCount > 0 && m.currentHypothesis != "" &&
       (len(m.tests) == 0 || m.tests[len(m.tests)-1].number != m.stats.testCount) {
        pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // серый
        rightContent.WriteString("\n" + pendingStyle.Render("⏳ Тест #"+fmt.Sprint(m.stats.testCount)+" в процессе..."))
        truncatedCurrent := truncateString(m.currentHypothesis, 55)
        rightContent.WriteString("\n  " + pendingStyle.Render(truncatedCurrent))
    }

    m.rightPanel.SetContent(rightContent.String())
    m.rightPanel.GotoBottom()
}

func (m *model) updateTable() {
    rows := []table.Row{
        {"Status", m.status},
        {"CPU", m.cpu},
        {"RAM", m.ram},
        {"Tests", fmt.Sprintf("%d", m.stats.testCount)},
    }

    if m.stats.lastWER > 0 {
        rows = append(rows, table.Row{"Last WER", fmt.Sprintf("%.1f%%", m.stats.lastWER*100)})
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
    b.WriteString(title + "\n")

    // Таблица статистики
    b.WriteString(m.table.View() + "\n")

    // Две панели рядом
    panels := lipgloss.JoinHorizontal(
        lipgloss.Top,
        m.leftPanel.View(),
        m.rightPanel.View(),
    )
    b.WriteString(panels + "\n")

    // Подсказки
    helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
    b.WriteString(helpStyle.Render("r: record/stop | s: test | c: clear | q: quit"))

    return b.String()
}

// --- Команды и горутины ---

func connectToServer(wsClient *lib.WSClient, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Println("Connecting to server...")
        if err := wsClient.Connect(); err != nil {
            log.Printf("Connection failed: %v", err)
            return wsErrorMsg(err)
        }
        log.Println("Connected to server, starting reader goroutine")
        go func() {
            for {
                msg, err := wsClient.ReadRawMessage()
                if err != nil {
                    log.Printf("WebSocket read error: %v", err)
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
        log.Printf("Creating recorder with sampleRate=%d, chunkMs=%d", sampleRate, chunkMs)
        recorder, err := lib.NewRecorder(sampleRate, chunkMs)
        if err != nil {
            log.Printf("Failed to create recorder: %v", err)
            return recorderErrorMsg(err)
        }
        log.Printf("Recorder created successfully")

        // Запускаем горутину для чтения чанков
        go func() {
            ch := recorder.GetChunkChan()
            for chunk := range ch {
                if program != nil {
                    program.Send(audioChunkMsg(chunk))
                }
            }
            log.Printf("Chunk channel closed")
        }()

        return recorderCreatedMsg{recorder: recorder}
    }
}

func startRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Printf("Starting recording...")
        if err := recorder.StartRecording(); err != nil {
            log.Printf("Failed to start recording: %v", err)
            return recorderErrorMsg(err)
        }
        log.Printf("Recording started successfully")
        return recorderStartedMsg{}
    }
}

func stopRecording(recorder *lib.Recorder, program *tea.Program) tea.Cmd {
    return func() tea.Msg {
        log.Printf("Stopping recording...")
        data, err := recorder.StopRecording()
        if err != nil {
            log.Printf("Failed to stop recording: %v", err)
            return recorderErrorMsg(err)
        }
        log.Printf("Recording stopped, %d bytes captured", len(data))
        return recordingDoneMsg{data: data, size: len(data)}
    }
}
