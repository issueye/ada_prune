package desktop

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_proxy/internal/app"
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

	state := newUIState(a)
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
		mygo.App.Quit()
	}

	newWindow := func() *mygo.Window {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:  "AdaProxy",
			Width:  1280,
			Height: 860,
			// Room for the sidebar and two card columns.
			MinWidth: 960, MinHeight: 640,
			// The view draws the title bar itself (see uiState.titleBar).
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  40,
			BackgroundColor: "#f2f4f7",
			Content:         ui.View(state.view),
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
		state.reloadQuiet()

		installed, trayErr := setUpTray(showWindow, quit)
		if trayErr != nil {
			// A session without a tray is still a working app.
			state.flash("系统托盘不可用: "+trayErr.Error(), true)
		} else {
			tray = installed
		}

		// A slow poll keeps the dashboard honest without a visible spinner.
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if win == nil || win.IsDestroyed() {
					return
				}
				win.Update(func() {
					if state.page == "/" || state.page == "/logs" {
						state.reloadQuiet()
					}
				})
			}
		}()
	})

	if err := mygo.App.Run(); err != nil {
		return err
	}
	quitting = true
	return nil
}
