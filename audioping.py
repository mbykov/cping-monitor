import asyncio
import json
import yaml
import ssl
import time
import psutil
import pyaudio
import websockets
from jiwer import wer
from textual.app import App, ComposeResult
from textual.widgets import Header, Footer, Static, Label, ListView, ListItem
from textual.containers import Container, Horizontal, Vertical
from textual.reactive import reactive

# Загрузка конфигурации
with open("config.yaml", "r") as f:
    config = yaml.safe_load(f)

# Настройки аудио (Float32 как ждет сервер)
CHUNK = 1024
FORMAT = pyaudio.paFloat32
CHANNELS = 1
RATE = 16000

class CPingApp(App):
    CSS = """
    Screen { layout: vertical; }
    #stats-bar { height: 3; background: $accent; color: white; padding: 0 1; content-align: center middle; text-style: bold; }
    #interim-area { height: 3; background: $panel; border: tall $accent; margin: 0 1; padding: 0 1; color: $text; }
    .column-title { background: $primary; color: white; text-align: center; text-style: bold; height: 1; margin: 0 1; }
    #main-container { layout: horizontal; height: 1fr; }
    .column { width: 50%; border: solid $primary; margin: 0 1; height: 100%; }
    #left-pane, #right-pane { height: 1fr; overflow-y: scroll; background: $surface; }
    .log-card { border: solid $secondary; margin: 0 0 1 0; padding: 0 1; background: $surface; color: $text; }
    """

    BINDINGS = [
        ("r", "toggle_record", "Record/Stop"),
        ("s", "send_ping", "Send Ping"),
        ("c", "clear_all", "Clear"),
        ("q", "quit", "Quit"),
    ]

    # Реактивные переменные
    status = reactive("IDLE")
    interim_text = reactive("")

    def __init__(self):
        super().__init__()
        self.audio = pyaudio.PyAudio()
        self.is_recording = False
        self.audio_buffer = []
        self.reference_text = ""
        self.current_trial_text = ""
        self.ws = None
        self.start_time = 0

    def compose(self) -> ComposeResult:
        yield Header()
        yield Static("Initializing stats...", id="stats-bar")
        yield Static("[INTERIM]: ", id="interim-area")
        with Container(id="main-container"):
            with Vertical(classes="column"):
                yield Label("LIVE SERVER RESPONSES", classes="column-title")
                yield ListView(id="left-pane")
            with Vertical(classes="column"):
                yield Label("BENCHMARK (WER / TIME)", classes="column-title")
                yield ListView(id="right-pane")
        yield Footer()

    async def on_mount(self):
        self.set_interval(1.0, self.update_sys_stats)
        # Запускаем воркеры через асинхронные задачи
        self.run_worker(self.ws_client(), thread=False)
        self.run_worker(self.mic_worker(), thread=True) # Микрофон в отдельный поток для стабильности

    def update_sys_stats(self):
        cpu = psutil.cpu_percent()
        ram = psutil.virtual_memory().percent
        stats = f"CPU: {cpu}% | RAM: {ram}% | Status: {self.status}"
        self.query_one("#stats-bar").update(stats)

    def watch_interim_text(self, text: str):
        self.query_one("#interim-area").update(f"[INTERIM]: {text}")

    async def ws_client(self):
        """Работа с WebSocket"""
        host = config['server'].get('host', 'localhost')
        uri = f"wss://{host}:{config['server']['port']}"

        ssl_ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        ssl_ctx.check_hostname = False
        ssl_ctx.verify_mode = ssl.CERT_NONE
        try:
            ssl_ctx.load_cert_chain(config['server']['cert'], config['server']['key'])
        except: pass

        try:
            async with websockets.connect(uri, ssl=ssl_ctx) as ws:
                self.ws = ws
                async for message in ws:
                    data = json.loads(message)
                    await self.handle_server_response(data)
        except Exception as e:
            self.notify(f"Connection Error: {e}", severity="error")

    async def handle_server_response(self, data):
        m_type = data.get("type", "").lower()
        text = data.get("text", "")

        if m_type == "interim":
            self.interim_text = text
        elif m_type in ["correct", "command", "final"]:
            self.interim_text = "" # Чистим interim

            # 1. Добавляем в левую панель (логи)
            kv_pairs = "\n".join([f"{k}: {v}" for k, v in data.items()])
            self.query_one("#left-pane").append(ListItem(Static(kv_pairs, classes="log-card")))
            self.query_one("#left-pane").scroll_end()

            # 2. Логика накопления текста
            if self.is_recording:
                # Формируем эталон (Reference)
                self.reference_text += " " + text
            else:
                # Идет процесс теста (S)
                self.current_trial_text += " " + text
                self.update_benchmark(self.current_trial_text.strip())

    def update_benchmark(self, trial_text):
        """Расчет WER и обновление правой панели"""
        if not self.reference_text.strip():
            return

        try:
            error = wer(self.reference_text.strip().lower(), trial_text.lower())
            duration = time.time() - self.start_time
            result_str = f"[WER: {error:.1%}] [Time: {duration:.2f}s]\nRef: {trial_text[:60]}..."

            # Обновляем или добавляем строку в бенчмарк
            # Для простоты — просто добавляем новую запись в конец
            self.query_one("#right-pane").append(ListItem(Static(result_str, classes="log-card")))
            self.query_one("#right-pane").scroll_end()
        except: pass

    async def mic_worker(self):
        """Захват микрофона"""
        stream = self.audio.open(format=FORMAT, channels=CHANNELS, rate=RATE, input=True, frames_per_buffer=CHUNK)
        while True:
            if stream.get_read_available() >= CHUNK:
                data = stream.read(CHUNK, exception_on_overflow=False)
                if self.is_recording and self.ws:
                    self.audio_buffer.append(data)
                    await self.ws.send(data)
            await asyncio.sleep(0.01)

    def action_toggle_record(self):
        if not self.is_recording:
            self.is_recording = True
            self.status = "RECORDING"
            self.audio_buffer = []
            self.reference_text = ""
            self.query_one("#left-pane").clear()
            self.notify("Recording started (Ref Mode)")
        else:
            self.is_recording = False
            self.status = "IDLE (REF READY)"
            self.notify("Recording stopped. Reference fixed.")

    async def action_send_ping(self):
        if not self.audio_buffer or not self.ws:
            self.notify("Nothing to send! Record first (R).")
            return

        self.status = "SENDING PING"
        self.current_trial_text = ""
        self.start_time = time.time()

        # Отправляем буфер
        for chunk in self.audio_buffer:
            await self.ws.send(chunk)
            await asyncio.sleep(CHUNK / RATE)

        self.status = "AWAITING RESULTS"

    def action_clear_all(self):
        self.audio_buffer = []
        self.reference_text = ""
        self.query_one("#left-pane").clear()
        self.query_one("#right-pane").clear()
        self.status = "CLEANED"
        self.notify("All cleared")

if __name__ == "__main__":
    app = CPingApp()
    app.run()
