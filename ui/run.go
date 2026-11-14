package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/app"
)

// Run starts the desktop application and blocks until it quits.
//
// The gateway runs in this process: closing the window hides it, the tray keeps
// it reachable, and only Quit stops the service.
func Run() error {
	// A second launch shows the running instance and exits.
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}

	userData, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return fmt.Errorf("desktop: resolve user data directory: %w", err)
	}
	a, err := app.New(app.Options{DataDir: filepath.Join(userData, "data")})
	if err != nil {
		return err
	}
	defer a.Close()

	// The gateway answers on this process's listener. Bind before the window
	// shows, so the footer and the tray advertise an address that actually
	// responds; app.New only prepares the server, the caller owns the listener.
	cfg := a.Cfg.Get()
	ln, boundPort, err := a.ListenScanning(cfg.Host, cfg.Port)
	if err != nil {
		return fmt.Errorf("desktop: bind %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- a.Serve(ln) }()

	state := newUIState(a)
	state.boundHost = cfg.Host
	state.boundPort = boundPort
	clipboardEndpoint = state.endpoint()

	var (
		win      *mygo.Window
		tray     *mygo.Tray
		quitting bool
	)

	showWindow := func() {
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Show()
		win.Focus()
	}

	quit := func() {
		quitting = true
		if tray != nil {
			tray.Destroy()
		}
		if win != nil && !win.IsDestroyed() {
			win.Close()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
		mygo.App.Quit()
	}

	newWindow := func() *mygo.Window {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:  "AdaPrune",
			Width:  1440,
			Height: 900,
			// Room for the sidebar and two card columns.
			MinWidth: 960, MinHeight: 640,
			// The view draws the title bar itself (see uiState.titleBar).
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  44,
			BackgroundColor: "#F1EFE8",
			Content:         gui.View(state.view),
		})
		w.OnClose(func(e *mygo.CloseEvent) {
			// Closing the window hides it: the gateway and the tray keep running.
			if !quitting {
				e.PreventDefault()
				w.Hide()
			}
		})
		state.win = w
		return w
	}

	mygo.App.WhenReady(func() {
		win = newWindow()
		if boundPort != cfg.Port {
			state.flash(fmt.Sprintf("端口 %d 被占用，服务改用 %d", cfg.Port, boundPort), true)
		}
		state.reloadQuiet()

		installed, trayErr := setUpTray(showWindow, quit)
		if trayErr != nil {
			// A session without a tray is still a working app.
			state.flash("系统托盘不可用: "+trayErr.Error(), true)
		} else {
			tray = installed
		}

		// A slow poll keeps the dashboard honest without a visible spinner. The
		// log tail honors its switch: off means the table only moves when the
		// user refreshes.
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if win == nil || win.IsDestroyed() {
					return
				}
				win.Update(func() {
					switch state.page {
					case "/":
						state.reloadQuiet()
					case "/logs":
						// The tail follows page 1 only: a reader on an older
						// page must not watch the rows shift under them.
						if state.liveTail && state.logPage == 1 {
							state.reloadQuiet()
						}
					}
				})
			}
		}()
	})

	// A serve failure (a listener gone bad, a panic in the HTTP stack) must not
	// pass silently: the window stays up, so surface why clients fail. Serve
	// returns nil on the quit path's Shutdown.
	go func() {
		serveErr := <-serveDone
		if serveErr == nil || quitting {
			return
		}
		if win != nil && !win.IsDestroyed() {
			win.Update(func() {
				state.flash("服务已停止: "+serveErr.Error(), true)
			})
		}
	}()

	if err := mygo.App.Run(); err != nil {
		return err
	}
	quitting = true
	return nil
}
