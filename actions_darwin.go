package main

// On macOS the same actions use Mac shortcuts. "Win" means the Command key.
func init() {
	zoomModVK = 0x5B
	builtinKeys = map[string]string{
		"taskview":    "Ctrl+Up",      // Mission Control
		"showdesktop": "F11",          // Show Desktop
		"desktopPrev": "Ctrl+Left",
		"desktopNext": "Ctrl+Right",
		"alttab":      "Cmd+Tab",
		"copy":        "Cmd+C",
		"cut":         "Cmd+X",
		"paste":       "Cmd+V",
		"undo":        "Cmd+Z",
		"redo":        "Cmd+Shift+Z",
		"selectall":   "Cmd+A",
		"save":        "Cmd+S",
		"find":        "Cmd+F",
		"screenshot":  "Cmd+Shift+4",
		"newtab":      "Cmd+T",
		"closetab":    "Cmd+W",
		"reopentab":   "Cmd+Shift+T",
		"nexttab":     "Ctrl+Tab",
		"prevtab":     "Ctrl+Shift+Tab",
		"browserback": "Cmd+[",
		"browserfwd":  "Cmd+]",
		"refresh":     "Cmd+R",
		"zoomin":      "Cmd+=",
		"zoomout":     "Cmd+-",
		"zoomreset":   "Cmd+0",
		"maximize":    "Ctrl+Cmd+F",
		"minimize":    "Cmd+M",
		"closewindow": "Cmd+W",
		"lock":        "Ctrl+Cmd+Q",
		"emoji":       "Ctrl+Cmd+Space",
		"explorer":    "Alt+Cmd+Space",
		"search":      "Cmd+Space",
		"appexpose":   "Ctrl+Down",
	}
}
