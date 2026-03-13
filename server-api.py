#!/usr/bin/env python3
"""
Мониторинг WebSocket сервера голосового дневника
Получает метрики через WebSocket API
"""

import asyncio
import websockets
import json
import time
import argparse
from datetime import datetime
from typing import Optional, Dict, Any, List
import signal
import sys
import os
import yaml
import ssl

class Config:
    """Загрузка конфигурации из YAML"""
    def __init__(self, config_path: str = "config.yaml"):
        self.config_path = config_path
        self.data = self.load()

    def load(self) -> Dict[str, Any]:
        """Загрузка YAML конфига"""
        try:
            with open(self.config_path, 'r') as f:
                return yaml.safe_load(f)
        except FileNotFoundError:
            print(f"❌ Файл конфига не найден: {self.config_path}")
            return {}
        except Exception as e:
            print(f"❌ Ошибка загрузки конфига: {e}")
            return {}

    @property
    def server_host(self) -> str:
        return self.data.get('server', {}).get('host', 'localhost')

    @property
    def server_port(self) -> int:
        return self.data.get('server', {}).get('port', 6006)

    @property
    def cert_path(self) -> Optional[str]:
        path = self.data.get('server', {}).get('cert')
        if path and os.path.exists(path):
            return path
        return None

    @property
    def key_path(self) -> Optional[str]:
        path = self.data.get('server', {}).get('key')
        if path and os.path.exists(path):
            return path
        return None

    @property
    def use_ssl(self) -> bool:
        return bool(self.cert_path and self.key_path)

class MetricsMonitor:
    def __init__(self, config: Config, interval: float = 2.0):
        self.config = config
        self.uri = self.build_uri()
        self.interval = interval
        self.running = True
        self.websocket: Optional[websockets.WebSocketClientProtocol] = None
        self.metrics_history = []
        self.max_history = 100
        self.last_metrics: Dict[str, Any] = {}
        self.ssl_context = self.create_ssl_context()
        self.stats = {
            "metrics_received": 0,
            "pongs_received": 0,
            "errors": 0,
            "last_pong": None,
            "start_time": time.time()
        }
        self.tasks = []

    def build_uri(self) -> str:
        """Создание URI из конфига"""
        protocol = "wss" if self.config.use_ssl else "ws"
        return f"{protocol}://{self.config.server_host}:{self.config.server_port}"

    def create_ssl_context(self) -> Optional[ssl.SSLContext]:
        """Создание SSL контекста с сертификатами"""
        if not self.config.use_ssl:
            return None

        # Для самоподписанных сертификатов
        ssl_context = ssl.create_default_context()
        ssl_context.check_hostname = False
        ssl_context.verify_mode = ssl.CERT_NONE

        # Загружаем клиентские сертификаты если есть
        if self.config.cert_path and self.config.key_path:
            try:
                ssl_context.load_cert_chain(
                    self.config.cert_path,
                    self.config.key_path
                )
                print(f"✅ Загружен сертификат: {self.config.cert_path}")
                print(f"✅ Загружен ключ: {self.config.key_path}")
            except Exception as e:
                print(f"⚠️ Ошибка загрузки сертификатов: {e}")

        return ssl_context

    async def connect(self):
        """Подключение к WebSocket серверу"""
        try:
            self.websocket = await websockets.connect(
                self.uri,
                ssl=self.ssl_context,
                user_agent_header="MetricsMonitor/1.0",
                ping_interval=20,
                ping_timeout=10,
                close_timeout=5,
                max_size=10_485_760
            )

            print(f"✅ Подключено к {self.uri}")

            if self.config.use_ssl:
                print(f"🔒 SSL включен")

            return True

        except Exception as e:
            print(f"❌ Ошибка подключения: {type(e).__name__}: {e}")
            return False

    async def send_command(self, cmd_type: str):
        """Отправка команды серверу"""
        if not self.websocket:
            return False

        try:
            await self.websocket.send(json.dumps({"type": cmd_type}))
            return True
        except Exception as e:
            print(f"❌ Ошибка отправки команды {cmd_type}: {e}")
            return False

    async def request_metrics(self):
        """Запрос метрик"""
        return await self.send_command("get_metrics")

    async def request_config(self):
        """Запрос конфигурации"""
        return await self.send_command("get_config")

    async def request_stats(self):
        """Запрос статистики"""
        return await self.send_command("get_stats")

    async def ping(self):
        """Отправка ping"""
        return await self.send_command("ping")

    async def request_gc(self):
        """Запрос на запуск сборщика мусора"""
        return await self.send_command("gc")

    async def listen(self):
        """Прослушивание сообщений от сервера"""
        if not self.websocket:
            return

        try:
            message = await asyncio.wait_for(
                self.websocket.recv(),
                timeout=self.interval + 1.0
            )

            # Сервер не должен отправлять бинарные данные в ответ на наши запросы
            if isinstance(message, bytes):
                print(f"⚠️ Получены бинарные данные: {len(message)} байт")
                return

            try:
                data = json.loads(message)
                msg_type = data.get("type")

                if msg_type == "metrics":
                    self.process_metrics(data)
                    self.stats["metrics_received"] += 1
                elif msg_type == "pong":
                    self.stats["pongs_received"] += 1
                    self.stats["last_pong"] = time.time()
                    # Не выводим каждый pong, чтобы не засорять экран
                elif msg_type == "config":
                    print(f"\n⚙️ Конфигурация получена:")
                    print(json.dumps(data.get("config", {}), indent=2, ensure_ascii=False))
                elif msg_type == "stats":
                    print(f"\n📊 Статистика получена:")
                    print(json.dumps(data.get("stats", {}), indent=2, ensure_ascii=False))
                elif msg_type == "gc_result":
                    print(f"\n🧹 Результат GC: освобождено {data.get('freed_mb', 0)} MB")
                elif msg_type in ["interim", "final", "correct", "command", "system"]:
                    # Эти типы сообщений игнорируем в режиме мониторинга
                    pass
                else:
                    print(f"\n📨 Неизвестный тип сообщения: {msg_type}")

            except json.JSONDecodeError:
                print(f"⚠️ Получено не-JSON сообщение: {message[:100]}...")

        except asyncio.TimeoutError:
            # Нормальный таймаут, продолжаем
            pass
        except websockets.exceptions.ConnectionClosedOK:
            print("✅ Соединение закрыто нормально")
            self.running = False
        except websockets.exceptions.ConnectionClosedError as e:
            print(f"❌ Соединение закрыто с ошибкой: {e}")
            self.running = False
        except Exception as e:
            print(f"❌ Ошибка приема: {type(e).__name__}: {e}")
            self.stats["errors"] += 1

    def process_metrics(self, data: Dict[str, Any]):
        """Обработка полученных метрик"""
        metrics = data.get("metrics", {})
        self.last_metrics = metrics
        self.metrics_history.append({
            "timestamp": time.time(),
            "metrics": metrics
        })

        if len(self.metrics_history) > self.max_history:
            self.metrics_history = self.metrics_history[-self.max_history:]

        self.display_metrics(metrics)

    def display_metrics(self, metrics: Dict[str, Any]):
        """Отображение метрик в консоли"""
        os.system('clear' if os.name == 'posix' else 'cls')

        uptime = time.time() - self.stats["start_time"]

        print("=" * 80)
        print(f"📊 МОНИТОРИНГ СЕРВЕРА - {datetime.now().strftime('%H:%M:%S')}")
        print(f"🔗 {self.uri}")
        print("=" * 80)

        # Статистика монитора
        print(f"\n📈 СТАТИСТИКА МОНИТОРА:")
        print(f"   Время работы:    {int(uptime // 60)}м {int(uptime % 60)}с")
        print(f"   Метрик получено: {self.stats['metrics_received']}")
        print(f"   Pong получено:   {self.stats['pongs_received']}")
        if self.stats['last_pong']:
            last_pong = datetime.fromtimestamp(self.stats['last_pong']).strftime('%H:%M:%S')
            print(f"   Последний pong:  {last_pong}")
        print(f"   Ошибок:          {self.stats['errors']}")

        # Память
        mem = metrics.get("memory", {})
        print("\n💾 ПАМЯТЬ:")
        print(f"   Alloc:        {mem.get('alloc_mb', 0):6d} MB")
        print(f"   Heap:         {mem.get('heap_mb', 0):6d} MB")
        print(f"   Sys:          {mem.get('sys_mb', 0):6d} MB")
        print(f"   Goroutines:   {mem.get('goroutines', 0):6d}")
        print(f"   GC циклов:    {mem.get('gc_cycles', 0):6d}")
        if 'gc_pause_total_ms' in mem:
            print(f"   GC пауза:     {mem.get('gc_pause_total_ms', 0):6d} ms")

        # Сессия
        sess = metrics.get("session", {})
        print("\n🔌 СЕССИЯ:")
        print(f"   ID:              {sess.get('id', 'N/A')}")
        print(f"   Команд найдено:  {sess.get('commands_found', 0):6d}")
        print(f"   Фреймов:         {sess.get('frames_processed', 0):6d}")
        if sess.get('audio_bytes_total', 0) > 0:
            print(f"   Аудио всего:     {sess.get('audio_bytes_total', 0)/1024:7.1f} KB")
        if sess.get('audio_buffered', 0) > 0:
            print(f"   Аудио в буфере:  {sess.get('audio_buffered', 0)/1024:7.1f} KB")

        # Vosk
        vosk = metrics.get("vosk", {})
        if vosk.get('audio_processed', 0) > 0:
            print("\n🎤 VOSK:")
            print(f"   Аудио обработано: {vosk.get('audio_processed', 0)/1024:7.1f} KB")

        # GigaAM
        giga = metrics.get("gigaam", {})
        if giga:
            print("\n🤖 GIGAAM:")
            print(f"   Включен:    {giga.get('enabled', False)}")
            if giga.get('model'):
                model_path = giga.get('model', '')
                short_path = os.path.basename(model_path) if model_path else ''
                print(f"   Модель:     {short_path}")

        # Тренды
        if len(self.metrics_history) > 1:
            self.show_trends()

        # Подсказки по командам
        print("\n" + "=" * 80)
        print("🔄 Команды: [m]етрики [p]ing [c]onfig [s]tats [g]c [q]uit")
        print(f"⏱️  Обновление каждые {self.interval} сек | Нажмите Ctrl+C для выхода")

    def show_trends(self):
        """Показывает тренды метрик за последние N замеров"""
        if len(self.metrics_history) < 5:
            return

        recent = self.metrics_history[-5:]

        first_mem = recent[0]["metrics"].get("memory", {}).get("alloc_mb", 0)
        last_mem = recent[-1]["metrics"].get("memory", {}).get("alloc_mb", 0)

        first_gor = recent[0]["metrics"].get("memory", {}).get("goroutines", 0)
        last_gor = recent[-1]["metrics"].get("memory", {}).get("goroutines", 0)

        first_gc = recent[0]["metrics"].get("memory", {}).get("gc_cycles", 0)
        last_gc = recent[-1]["metrics"].get("memory", {}).get("gc_cycles", 0)

        print("\n📈 ТРЕНДЫ (последние 5 замеров):")

        # Память
        if last_mem > first_mem:
            diff = last_mem - first_mem
            rate = diff / len(recent)
            print(f"   ⚠️ Память: +{diff} MB  (+{rate:.1f} MB/замер)")
        elif last_mem < first_mem:
            diff = first_mem - last_mem
            print(f"   📉 Память: -{diff} MB")
        else:
            print(f"   ➡️ Память: {last_mem} MB")

        # Горутины
        if last_gor > first_gor:
            print(f"   ⚠️ Горутины: +{last_gor - first_gor}")
        elif last_gor < first_gor:
            print(f"   ✅ Горутины: -{first_gor - last_gor}")

        # GC
        if last_gc > first_gc:
            print(f"   🔄 GC циклов: +{last_gc - first_gc}")

    async def handle_keyboard(self):
        """Обработка клавиатурного ввода"""
        loop = asyncio.get_running_loop()

        while self.running:
            try:
                # Асинхронное чтение с клавиатуры (неблокирующее)
                if sys.stdin in loop._selector.get_map() if hasattr(loop, '_selector') else False:
                    key = await loop.run_in_executor(None, sys.stdin.read, 1)

                    if key == 'm':
                        await self.request_metrics()
                    elif key == 'p':
                        await self.ping()
                        print("\n🏓 Ping отправлен")
                    elif key == 'c':
                        await self.request_config()
                    elif key == 's':
                        await self.request_stats()
                    elif key == 'g':
                        await self.request_gc()
                    elif key == 'q':
                        print("\n👋 Завершение по запросу")
                        self.running = False
                        break
            except (IOError, AttributeError, RuntimeError):
                # Игнорируем ошибки ввода (например, когда stdin не в режиме неблокирующего чтения)
                pass
            except Exception as e:
                # Любые другие ошибки ввода игнорируем
                pass

            await asyncio.sleep(0.1)

    async def run(self):
        """Основной цикл мониторинга"""
        print(f"\n🔧 Конфигурация:")
        print(f"   Хост: {self.config.server_host}")
        print(f"   Порт: {self.config.server_port}")
        print(f"   SSL:  {'включен' if self.config.use_ssl else 'выключен'}")
        print()

        # Подключаемся
        if not await self.connect():
            print("\n💡 Не удалось подключиться. Проверьте:")
            print("   1. Запущен ли сервер (go run main.go)")
            print("   2. Правильные ли пути в config.yaml")
            print("   3. Не блокирует ли фаервол")
            return

        # Запускаем обработку клавиатуры (опционально)
        # keyboard_task = asyncio.create_task(self.handle_keyboard())
        # self.tasks.append(keyboard_task)

        try:
            # Отправляем тестовый ping
            await self.ping()
            await asyncio.sleep(1)

            # Основной цикл
            while self.running:
                # Запрашиваем метрики
                if not await self.request_metrics():
                    print("⚠️ Не удалось запросить метрики, переподключаемся...")
                    if not await self.connect():
                        await asyncio.sleep(5)
                        continue

                # Слушаем ответы в течение интервала
                start_time = time.time()
                while time.time() - start_time < self.interval and self.running:
                    await self.listen()
                    await asyncio.sleep(0.05)

        except asyncio.CancelledError:
            # Нормальное завершение по Ctrl+C
            pass
        finally:
            await self.shutdown()

    async def shutdown(self):
        """Корректное завершение"""
        if not self.running:
            return

        print("\n\n🛑 Завершение работы...")
        self.running = False

        # Отменяем все задачи
        for task in self.tasks:
            if not task.done():
                task.cancel()

        # Ждем завершения задач
        if self.tasks:
            await asyncio.gather(*self.tasks, return_exceptions=True)

        # Закрываем соединение
        if self.websocket:
            try:
                await self.websocket.close()
            except:
                pass

        await asyncio.sleep(0.5)

        print("\n📊 Итоговая статистика:")
        print(f"   Метрик получено: {self.stats['metrics_received']}")
        print(f"   Pong получено:   {self.stats['pongs_received']}")
        print(f"   Ошибок:          {self.stats['errors']}")
        print(f"   Время работы:    {int((time.time() - self.stats['start_time']) // 60)}м")
        print("\n✅ Мониторинг остановлен")

async def main():
    parser = argparse.ArgumentParser(description='Мониторинг WebSocket сервера')
    parser.add_argument('--config', default='config.yaml', help='Путь к config.yaml')
    parser.add_argument('--interval', type=float, default=2.0, help='Интервал опроса (сек)')
    parser.add_argument('--no-ssl', action='store_true', help='Отключить SSL (ws://)')
    parser.add_argument('--host', help='Переопределить хост')
    parser.add_argument('--port', type=int, help='Переопределить порт')
    parser.add_argument('--no-keyboard', action='store_true', help='Отключить интерактивный ввод')

    args = parser.parse_args()

    # Загружаем конфиг
    config = Config(args.config)

    # Переопределяем параметры если нужно
    if args.no_ssl or args.host or args.port:
        class OverrideConfig:
            def __init__(self, base_config, host=None, port=None, no_ssl=False):
                self.base = base_config
                self._host = host
                self._port = port
                self._no_ssl = no_ssl
            @property
            def server_host(self): return self._host or self.base.server_host
            @property
            def server_port(self): return self._port or self.base.server_port
            @property
            def use_ssl(self): return False if self._no_ssl else self.base.use_ssl
            @property
            def cert_path(self): return None if self._no_ssl else self.base.cert_path
            @property
            def key_path(self): return None if self._no_ssl else self.base.key_path

        config = OverrideConfig(config, args.host, args.port, args.no_ssl)

    monitor = MetricsMonitor(config, args.interval)

    try:
        await monitor.run()
    except asyncio.CancelledError:
        # Это ожидаемо при Ctrl+C
        pass
    except Exception as e:
        print(f"❌ Критическая ошибка: {e}")
        import traceback
        traceback.print_exc()
        return 1

    return 0

if __name__ == "__main__":
    try:
        exit_code = asyncio.run(main())
        sys.exit(exit_code)
    except KeyboardInterrupt:
        # Нормальное завершение
        print("\n\n👋 Завершено пользователем")
        sys.exit(0)
    except SystemExit as e:
        # Пробрасываем код выхода
        sys.exit(e.code)
    except Exception as e:
        print(f"❌ Ошибка: {e}")
        sys.exit(1)
