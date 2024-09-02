package main

import (
	"fmt"
	"time"

	"github.com/alecthomas/kong"
)

var Config struct {
	KeypressesPerWindow int `env:"KEYPRESSES_PER_WINDOW" default:"100000" help:"How many keypresses to accumulate within a single window before creating a new window."`

	FifoPath  string `env:"FIFO_PATH" required:"" help:"Path to the FIFO; will be created if it does not exist."`
	FifoGroup string `env:"FIFO_GROUP" help:"Linux user group to set on the FIFO"`
	FifoMode  string `env:"FIFO_MODE" default:"0620" help:"Permissions for the FIFO"`

	CacheFilePath       string        `env:"CACHE_FILE" default:"/var/cache/kmonad-keylogger/partial_map.json" help:"Path to the file used for caching the window values, until the window is filled. This is necessary to persist partial windows after shutdowns, and also helps recover from crashes."`
	CacheWriteFrequency time.Duration `env:"CACHE_WRITE_FREQUENCY" default:"30s" help:"How often to write to the cache file"`

	DestinationDirPath  string `env:"DESTINATION_DIR" required:"" help:"Directory to write full window data files into."`
	DestinationDirGroup string `env:"DESTINATION_DIR_GROUP" help:"Linux user group to set on the destination directory, as well as the data files."`
	DestinationDirMode  string `env:"DESTINATION_DIR_FILE_MODE" default:"0440" help:"File permissions on the destination directory, as well as the data files."`
}

func main() {
	_ = kong.Parse(&Config)
	fmt.Println(Config)
}
