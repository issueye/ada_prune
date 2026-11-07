package desktop

import (
	_ "embed"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

// trayIcon is the 32×32 PNG the tray shows; the 2×/64px copy is used on high DPI
// panels where the platform asks for it.
//
//go:embed tray.png
var trayIconPNG []byte

// setUpTray installs the tray icon and its menu. It returns nil when the platform
// offers no tray (some Linux sessions), which must not stop the app.
func setUpTray(onShow, onQuit func()) (*mygo.Tray, error) {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "adaproxy",
		Icon:    trayIconPNG,
		ToolTip: "AdaProxy · 本地 AI 网关",
		Title:   "AdaProxy",
		Menu:    trayMenu(onShow, onQuit),
	})
	if err != nil {
		return nil, err
	}
	// A left click shows the window, as operators expect from a status icon.
	tray.OnClick(onShow)
	return tray, nil
}

// trayMenu is the tray's menu: show the window, copy the endpoint, quit.
func trayMenu(onShow, onQuit func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开管理界面", Click: func(*mygo.MenuItem, *mygo.Window) { onShow() }},
		{Label: "复制服务地址", Click: func(*mygo.MenuItem, *mygo.Window) { copyEndpoint() }},
		mygo.Separator(),
		{Label: "退出 AdaProxy", Click: func(*mygo.MenuItem, *mygo.Window) { onQuit() }},
	})
}

// clipboardEndpoint is set by the running app so the tray can copy it.
var clipboardEndpoint = ""

// copyEndpoint copies the listener address to the clipboard.
func copyEndpoint() {
	if clipboardEndpoint == "" {
		return
	}
	data := transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte(clipboardEndpoint))))
	_ = mygo.Clipboard.Write(data)
}
