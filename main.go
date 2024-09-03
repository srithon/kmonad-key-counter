package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/alecthomas/kong"
)

var Config struct {
	MaxKeypressesPerWindow int `env:"MAX_KEYPRESSES_PER_WINDOW" default:"100000" help:"How many keypresses to accumulate within a single window before creating a new window."`

	FifoPath  string `env:"FIFO_PATH" default:"/run/kmonad-keylogger.sock" help:"Path to the FIFO; will be created if it does not exist."`
	FifoGroup string `env:"FIFO_GROUP" help:"Linux user group to set on the FIFO"`
	// 400 = 0620
	FifoMode os.FileMode `env:"FIFO_MODE" default:"400" help:"Permissions for the FIFO"`

	CacheFilePath       string        `env:"CACHE_FILE" default:"/var/cache/kmonad-keylogger/partial_map.json" help:"Path to the file used for caching the window values, until the window is filled. This is necessary to persist partial windows after shutdowns, and also helps recover from crashes."`
	CacheWriteFrequency time.Duration `env:"CACHE_WRITE_FREQUENCY" default:"30s" help:"How often to write to the cache file"`

	DestinationDirPath  string `env:"DESTINATION_DIR" default:"/var/local/kmonad-keylogger" help:"Directory to write full window data files into."`
	DestinationDirGroup string `env:"DESTINATION_DIR_GROUP" help:"Linux user group to set on the destination directory, as well as the data files."`
	// 288 = 0440
	DestinationDirMode os.FileMode `env:"DESTINATION_DIR_FILE_MODE" default:"288" help:"File permissions on the destination directory, as well as the data files."`
}

type WindowState struct {
	// When the window started
	WindowStartTime time.Time `json:"start_ts"`

	// When the window ended; will be uninitialized initially
	WindowEndTime time.Time `json:"end_ts"`

	// Keeps track of the sum of the sum of values for the keyFrequencies map.
	TotalKeyPresses int `json:"total"`

	// Keeps track of how often each key was pressed.
	KeyFrequencies map[string]int `json:"frequencies"`
}

func newWindowState() WindowState {
	var state WindowState
	state.WindowStartTime = time.Now()
	state.KeyFrequencies = make(map[string]int)
	return state
}

func main() {
	_ = kong.Parse(&Config)

	slog.SetLogLoggerLevel(slog.LevelDebug)

	// 0. validate config options
	if Config.CacheWriteFrequency < 5*time.Second {
		slog.Error(
			"Cache write frequency is too high! Delay must be 5s or more.",
			"frequency",
			Config.CacheWriteFrequency,
		)
		os.Exit(1)
	}

	if Config.MaxKeypressesPerWindow < 1 {
		slog.Error(
			"Max keypresses per window must a be a non-zero positive integer!",
			"maxKeypressesPerWindow",
			Config.MaxKeypressesPerWindow,
		)
		os.Exit(1)
	}

	// 0.5. set umask
	unix.Umask(0)

	// 1. load cache file

	// create directory if it doesn't exist
	userRwMode := fs.FileMode(int(0600))
	cacheDir := path.Dir(Config.CacheFilePath)
	err := os.MkdirAll(cacheDir, userRwMode)
	if err != nil {
		slog.Error("Failed to create cache directory!", "error", err)
		os.Exit(1)
	}

	cacheFile, err := os.OpenFile(Config.CacheFilePath, os.O_RDWR|os.O_CREATE, userRwMode)
	if err != nil {
		slog.Error("Could not open cache file!", "error", err)
		os.Exit(1)
	}

	defer cacheFile.Close()

	windowState, err := ReadCache(cacheFile)
	if err != nil {
		slog.Info("Could not parse cache on startup; starting with empty state")
		slog.Debug("Cache parse error", "error", err)
		windowState = newWindowState()
	}

	// 2. bind to FIFO
	fifoFile, err := BindToFifo(Config.FifoPath, Config.FifoMode, Config.FifoGroup)
	if err != nil {
		slog.Error("Failed to bind to FIFO", "error", err)
		os.Exit(1)
	}

	defer fifoFile.Close()

	// 3. set up cache write timer
	cacheTimer := time.NewTicker(Config.CacheWriteFrequency)

	// 4. set up Fifo channel
	keypressChannel := ListenFifo(fifoFile)

	// 5. run main loop
	cacheInvalidated := false
	for {
		select {
		case currentTime := <-cacheTimer.C:
			if !cacheInvalidated {
				continue
			}

			if err := cacheFile.Truncate(0); err != nil {
				panic(fmt.Errorf("Failed to truncate cache file: %w", err))
			}

			// necessary because File.Truncate doesn't shift I/O offset;
			// os.Truncate does though
			if _, err := cacheFile.Seek(0, 0); err != nil {
				panic(fmt.Errorf("Failed to seek cache file position to 0: %w", err))
			}

			if err := WriteWindowState(&windowState, cacheFile); err != nil {
				panic(fmt.Errorf("Failed to write cache window state: %w", err))
			}

			slog.Debug("Updated cache!", "time", currentTime)
			cacheInvalidated = false
		case token := <-keypressChannel:
			windowState.TotalKeyPresses += 1

			frequency, exists := windowState.KeyFrequencies[token]
			if exists {
				frequency += 1
			} else {
				frequency = 1
			}

			windowState.KeyFrequencies[token] = frequency

			if windowState.TotalKeyPresses >= Config.MaxKeypressesPerWindow {
				windowState.WindowEndTime = time.Now()
				filename := fmt.Sprintf(
					"%d_to_%d.json",
					windowState.WindowStartTime.Unix(),
					windowState.WindowEndTime.Unix(),
				)

				// write current map
				filename = path.Join(Config.DestinationDirPath, filename)
				file, err := os.OpenFile(
					filename,
					os.O_WRONLY|os.O_CREATE,
					Config.DestinationDirMode,
				)
				if err != nil {
					slog.Error("Failed to open destination file for writing!", "error", err)
					os.Exit(1)
				}

				if err := WriteWindowState(&windowState, file); err != nil {
					slog.Error("Failed to write full window state", "error", err)
					// TODO: figure out something better to do than exit the program
					os.Exit(1)
				}

				// now, make a new map
				windowState = newWindowState()
			}

			cacheInvalidated = true
		}
	}
}

func ReadCache(cacheFile *os.File) (WindowState, error) {
	var windowState WindowState

	cacheContent, err := io.ReadAll(cacheFile)
	if err != nil {
		slog.Error("Failed to read cache file!", "error", err)
		return windowState, err
	}

	err = json.Unmarshal(cacheContent, &windowState)
	return windowState, err
}

func BindToFifo(fifoPath string, fifoMode os.FileMode, fifoGroupName string) (*os.File, error) {
	// first, check if the file already exists as a FIFO, creating it if
	// necessary
	stat, err := os.Lstat(fifoPath)
	if err == nil {
		if stat.Mode()&os.ModeNamedPipe == 0 {
			return nil, fmt.Errorf("File already exists at fifo path and is not a fifo!")
		}
	} else {
		err := unix.Mkfifo(Config.FifoPath, uint32(Config.FifoMode))
		if err != nil {
			return nil, fmt.Errorf("Failed to create FIFO: %w", err)
		}
	}

	runningUserId := unix.Getuid()
	fifoGroupId := runningUserId
	if Config.FifoGroup != "" {
		fifoGroup, err := user.LookupGroup(Config.FifoGroup)
		if err != nil {
			return nil, fmt.Errorf("Group %s does not exist", Config.FifoGroup)
		}

		fifoGroupId, err = strconv.Atoi(fifoGroup.Gid)
		if err != nil {
			// this should never happen
			slog.Error("Group id is not an integer", "groupId", fifoGroup.Gid)
			panic(fmt.Sprintf("Group id %s is not an integer", fifoGroup.Gid))
		}
	}

	err = unix.Chown(Config.FifoPath, runningUserId, fifoGroupId)
	fifoPipe, err := os.OpenFile(Config.FifoPath, os.O_RDONLY, Config.FifoMode)
	if err != nil {
		return nil, fmt.Errorf("Failed to open newly created fifo: %w", err)
	}

	return fifoPipe, nil
}

func ListenFifo(fifoFile *os.File) chan string {
	keypressChannel := make(chan string)
	// need to open a null writer for the FIFO so that we don't receive EOF
	// when any of the real writers disconnect
	nullWriter, err := os.OpenFile(fifoFile.Name(), os.O_WRONLY, 0)
	if err != nil {
		panic(fmt.Errorf("Failed to open null writer for fifo: %w", err))
	}

	go func() {
		defer nullWriter.Close()
		scanner := bufio.NewScanner(fifoFile)
		for scanner.Scan() {
			token := scanner.Text()
			keypressChannel <- token
		}
	}()

	return keypressChannel
}

func WriteWindowState(windowState *WindowState, file *os.File) error {
	stateJson, err := json.Marshal(windowState)
	if err != nil {
		return fmt.Errorf("Failed to convert window state to json: %w", err)
	}

	_, err = file.Write(stateJson)
	if err != nil {
		err = fmt.Errorf("Failed to write json to destination file: %w", err)
	}

	return err
}
