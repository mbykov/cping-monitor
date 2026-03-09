package lib

import (
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type Config struct {
    Server struct {
        Host     string `yaml:"host"`
        Port     int    `yaml:"port"`
        Cert     string `yaml:"cert"`
        UseHTTPS bool   `yaml:"use_https"`
    } `yaml:"server"`

    Audio struct {
        Source     string `yaml:"source"` // "microphone" или "file"
        FilePath   string `yaml:"file_path"`
        SampleRate int    `yaml:"sample_rate"`
        ChunkMs    int    `yaml:"chunk_ms"`   // размер чанка для отправки
        SilenceMs  int    `yaml:"silence_ms"` // тишина для определения конца
    } `yaml:"audio"`

    Test struct {
        Repeat   int           `yaml:"repeat"`    // количество повторений
        Interval time.Duration `yaml:"interval"`  // интервал между повторениями
        Warmup   int           `yaml:"warmup"`    // прогрев перед замерами
    } `yaml:"test"`

    Output struct {
        SaveAudio bool   `yaml:"save_audio"`
        SaveDir   string `yaml:"save_dir"`
        LogLevel  string `yaml:"log_level"`
    } `yaml:"output"`
}

func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var cfg Config
    err = yaml.Unmarshal(data, &cfg)
    return &cfg, err
}
