// Native macOS layer for Mishk: menu-bar icon, settings window (WKWebView),
// Logitech HID++ access through IOKit, synthetic input through CGEvent,
// and an event tap for the extra buttons of any mouse.
#include <stdint.h>

void macRun(const char *html, int showWindow);
void macShowWindow(void);
void macQuit(void);
void macEval(const char *js);
void macSetStatus(const char *tooltip, const char *menuLine, int paused, const char *openTitle, const char *pauseTitle, const char *quitTitle);
void macSetDark(int dark);

int macHIDWrite(uintptr_t dev, const uint8_t *data, int len);

void macKey(int keycode, int down, uint64_t flags);
void macMediaKey(int key, int down);
void macMouseButton(int button, int down);
void macScroll(int dy, int dx);
char *macFrontApp(void);
char *macRunningApps(void);
int macSetTap(int on);
int macAccessibilityTrusted(int prompt);
void macStartHID(void);
void macHIDRelease(uintptr_t dev);
