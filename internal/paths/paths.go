// Package paths resolves the data directory and every file the process writes.
//
// It deliberately imports nothing but the standard library: the headless
// server must build without the desktop stack.
package paths

import (
	"os"
	"path/filepath"
)

// Dirs is the resolved set of files and directories for one data root.
type Dirs struct {
	// Data is the root directory. Everything else lives under it.
	Data string
	// DB is the SQLite file.
	DB string
	// SecretKey is the AES-256-GCM key file.
	SecretKey string
	// Config is the JSON configuration file.
	Config string
	// Logs is the directory holding the line log.
	Logs string
	// LogFile is the default log file path.
	LogFile string
}

// Resolve turns a data directory into the full set of paths.
//
// Precedence: the ADP_DATA_DIR environment variable, then dataDir, then "data"
// relative to the working directory. The directories are not created here;
// callers create them when they open the files.
func Resolve(dataDir string) Dirs {
	root := os.Getenv("ADP_DATA_DIR")
	if root == "" {
		root = dataDir
	}
	if root == "" {
		root = filepath.Join(ExecutableDir(), "data")
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	logs := filepath.Join(root, "logs")
	return Dirs{
		Data:      root,
		DB:        filepath.Join(root, "adaproxy.db"),
		SecretKey: filepath.Join(root, ".secret.key"),
		Config:    filepath.Join(root, "config.json"),
		Logs:      logs,
		LogFile:   filepath.Join(logs, "adaproxy.log"),
	}
}

// ExecutableDir returns the directory of the running executable, falling back
// to the working directory.
func ExecutableDir() string {
	exe, err := os.Executable()
	if err != nil {
		if wd, err2 := os.Getwd(); err2 == nil {
			return wd
		}
		return "."
	}
	return filepath.Dir(exe)
}
