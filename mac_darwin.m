// Native macOS layer for Mishk. See mac_darwin.h.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <IOKit/hid/IOHIDManager.h>
#import <ApplicationServices/ApplicationServices.h>
#include "mac_darwin.h"
#include "_cgo_export.h"

#define MISHK_MAGIC 0x4D49534BLL

// ---------------------------------------------------------------- window

@interface MishkApp : NSObject <NSApplicationDelegate, WKScriptMessageHandler, NSWindowDelegate, NSMenuDelegate>
@property (strong) NSWindow *window;
@property (strong) WKWebView *web;
@property (strong) NSStatusItem *status;
@property (strong) NSMenuItem *openItem, *lineItem, *pauseItem, *quitItem;
@property (strong) NSString *html;
@property BOOL showOnStart;
@end

static MishkApp *app;

@implementation MishkApp

- (void)applicationDidFinishLaunching:(NSNotification *)n {
    [self buildMainMenu];
    [self buildStatusItem];
    [[[NSWorkspace sharedWorkspace] notificationCenter] addObserver:self selector:@selector(didWake:)
        name:NSWorkspaceDidWakeNotification object:nil];
    goAppReady();
    if (self.showOnStart) [self showWindow];
}

- (void)didWake:(NSNotification *)n { goWake(); }

- (BOOL)applicationShouldHandleReopen:(NSApplication *)s hasVisibleWindows:(BOOL)v {
    [self showWindow];
    return YES;
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)s { return NO; }

- (void)buildMainMenu {
    // An Edit menu so copy / paste work in the window's text fields.
    NSMenu *bar = [NSMenu new];
    NSMenuItem *appItem = [NSMenuItem new];
    [bar addItem:appItem];
    NSMenu *appMenu = [NSMenu new];
    [appMenu addItemWithTitle:@"Hide Mishk" action:@selector(hide:) keyEquivalent:@"h"];
    [appMenu addItemWithTitle:@"Quit Mishk" action:@selector(menuQuit:) keyEquivalent:@"q"];
    appItem.submenu = appMenu;
    NSMenuItem *editItem = [NSMenuItem new];
    [bar addItem:editItem];
    NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
    [edit addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
    [edit addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"Z"];
    [edit addItem:[NSMenuItem separatorItem]];
    [edit addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
    [edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
    [edit addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
    [edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
    editItem.submenu = edit;
    NSMenuItem *winItem = [NSMenuItem new];
    [bar addItem:winItem];
    NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
    [win addItemWithTitle:@"Close" action:@selector(performClose:) keyEquivalent:@"w"];
    [win addItemWithTitle:@"Minimize" action:@selector(performMiniaturize:) keyEquivalent:@"m"];
    winItem.submenu = win;
    NSApp.mainMenu = bar;
}

- (void)buildStatusItem {
    self.status = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
    NSImage *img = nil;
    if (@available(macOS 11.0, *)) {
        img = [NSImage imageWithSystemSymbolName:@"computermouse" accessibilityDescription:@"Mishk"];
    }
    if (img) {
        img.template = YES;
        self.status.button.image = img;
    } else {
        self.status.button.title = @"M";
    }
    NSMenu *m = [NSMenu new];
    m.delegate = self;
    self.openItem = [[NSMenuItem alloc] initWithTitle:@"Open settings" action:@selector(menuOpen:) keyEquivalent:@""];
    self.lineItem = [[NSMenuItem alloc] initWithTitle:@"Mishk" action:nil keyEquivalent:@""];
    self.lineItem.enabled = NO;
    self.pauseItem = [[NSMenuItem alloc] initWithTitle:@"Pause button remapping" action:@selector(menuPause:) keyEquivalent:@""];
    self.quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit" action:@selector(menuQuit:) keyEquivalent:@""];
    for (NSMenuItem *i in @[self.openItem, self.pauseItem, self.quitItem]) i.target = self;
    [m addItem:self.openItem];
    [m addItem:[NSMenuItem separatorItem]];
    [m addItem:self.lineItem];
    [m addItem:self.pauseItem];
    [m addItem:[NSMenuItem separatorItem]];
    [m addItem:self.quitItem];
    m.autoenablesItems = NO;
    self.status.menu = m;
}

- (void)menuOpen:(id)s { [self showWindow]; }
- (void)menuPause:(id)s { goMenu(2); }
- (void)menuQuit:(id)s { goMenu(3); }

- (void)showWindow {
    if (!self.window) {
        NSRect r = NSMakeRect(0, 0, 1120, 760);
        self.window = [[NSWindow alloc] initWithContentRect:r
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
            backing:NSBackingStoreBuffered defer:NO];
        self.window.title = @"Mishk";
        self.window.minSize = NSMakeSize(940, 640);
        self.window.releasedWhenClosed = NO;
        self.window.delegate = self;
        WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
        WKUserContentController *uc = [WKUserContentController new];
        [uc addScriptMessageHandler:self name:@"mishk"];
        NSString *bridge =
            @"(function(){var n=0,p={};"
             "window.mishkCall=function(cmd,arg){return new Promise(function(ok,bad){var id=++n;p[id]={ok:ok,bad:bad};"
             "window.webkit.messageHandlers.mishk.postMessage({id:id,cmd:cmd,arg:JSON.stringify(arg===undefined?null:arg)});});};"
             "window.__mishkReply=function(id,r){var c=p[id];delete p[id];if(!c)return;if(r&&r.error){c.bad(new Error(r.error));}else{c.ok(r?r.result:null);}};"
             "})();";
        [uc addUserScript:[[WKUserScript alloc] initWithSource:bridge injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
        cfg.userContentController = uc;
        self.web = [[WKWebView alloc] initWithFrame:r configuration:cfg];
        self.web.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        [self.web setValue:@NO forKey:@"drawsBackground"];
        self.window.contentView = self.web;
        [self.web loadHTMLString:self.html baseURL:[NSURL URLWithString:@"https://mishk.local/"]];
        [self.window center];
    }
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    [self.window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    goWindowShown(1);
}

- (void)windowWillClose:(NSNotification *)n {
    goWindowShown(0);
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

- (void)userContentController:(WKUserContentController *)uc didReceiveScriptMessage:(WKScriptMessage *)msg {
    NSDictionary *d = msg.body;
    if (![d isKindOfClass:[NSDictionary class]]) return;
    NSNumber *ident = d[@"id"];
    NSString *cmd = d[@"cmd"], *arg = d[@"arg"];
    char *res = goBridgeCall((char *)cmd.UTF8String, (char *)(arg ? arg.UTF8String : "null"));
    NSString *json = res ? [NSString stringWithUTF8String:res] : @"null";
    free(res);
    NSString *js = [NSString stringWithFormat:@"window.__mishkReply(%@,%@)", ident, json];
    [self.web evaluateJavaScript:js completionHandler:nil];
}
@end

void macRun(const char *html, int showWindow) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        app = [MishkApp new];
        app.html = [NSString stringWithUTF8String:html];
        app.showOnStart = showWindow != 0;
        NSApp.delegate = app;
        [NSApp run];
    }
}

void macShowWindow(void) { dispatch_async(dispatch_get_main_queue(), ^{ [app showWindow]; }); }

void macQuit(void) { dispatch_async(dispatch_get_main_queue(), ^{ [NSApp terminate:nil]; }); }

void macEval(const char *js) {
    NSString *s = [NSString stringWithUTF8String:js];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (app.web && app.window.visible) [app.web evaluateJavaScript:s completionHandler:nil];
    });
}

void macSetDark(int dark) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (app.window) app.window.appearance = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
    });
}

void macSetStatus(const char *tooltip, const char *menuLine, int paused, const char *openTitle, const char *pauseTitle, const char *quitTitle) {
    NSString *t = [NSString stringWithUTF8String:tooltip], *l = [NSString stringWithUTF8String:menuLine];
    NSString *o = [NSString stringWithUTF8String:openTitle], *p = [NSString stringWithUTF8String:pauseTitle], *q = [NSString stringWithUTF8String:quitTitle];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!app.status) return;
        app.status.button.toolTip = t;
        app.lineItem.title = l;
        app.openItem.title = o;
        app.pauseItem.title = p;
        app.quitItem.title = q;
        app.pauseItem.state = paused ? NSControlStateValueOn : NSControlStateValueOff;
    });
}

// ---------------------------------------------------------------- HID++ over IOKit

static IOHIDManagerRef hidManager;

static void hidInput(void *ctx, IOReturn result, void *sender, IOHIDReportType type, uint32_t reportID, uint8_t *report, CFIndex len) {
    if (result != kIOReturnSuccess || len <= 0) return;
    goHIDInput((uintptr_t)ctx, report, (int)len);
}

static int numberProp(IOHIDDeviceRef d, CFStringRef key) {
    CFTypeRef v = IOHIDDeviceGetProperty(d, key);
    int out = 0;
    if (v && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberIntType, &out);
    return out;
}

static void hidAdded(void *ctx, IOReturn result, void *sender, IOHIDDeviceRef dev) {
    int pid = numberProp(dev, CFSTR(kIOHIDProductIDKey));
    int page = numberProp(dev, CFSTR(kIOHIDPrimaryUsagePageKey));
    int outLen = numberProp(dev, CFSTR(kIOHIDMaxOutputReportSizeKey));
    int inLen = numberProp(dev, CFSTR(kIOHIDMaxInputReportSizeKey));
    char product[128] = "";
    CFTypeRef p = IOHIDDeviceGetProperty(dev, CFSTR(kIOHIDProductKey));
    if (p && CFGetTypeID(p) == CFStringGetTypeID()) CFStringGetCString((CFStringRef)p, product, sizeof product, kCFStringEncodingUTF8);
    if (IOHIDDeviceOpen(dev, kIOHIDOptionsTypeNone) != kIOReturnSuccess) return;
    CFRetain(dev);
    if (inLen < 20) inLen = 64;
    uint8_t *buf = calloc(1, inLen);
    IOHIDDeviceRegisterInputReportCallback(dev, buf, inLen, hidInput, dev);
    goHIDAdded((uintptr_t)dev, pid, page, outLen, product);
}

static void hidRemoved(void *ctx, IOReturn result, void *sender, IOHIDDeviceRef dev) {
    goHIDRemoved((uintptr_t)dev);
    // The device ref is released by Go once the engine has let go of it (see macHIDRelease).
}

void macHIDRelease(uintptr_t dev) {
    dispatch_async(dispatch_get_main_queue(), ^{ CFRelease((IOHIDDeviceRef)dev); });
}

int macHIDWrite(uintptr_t dev, const uint8_t *data, int len) {
    IOReturn r = IOHIDDeviceSetReport((IOHIDDeviceRef)dev, kIOHIDReportTypeOutput, data[0], data, len);
    return r == kIOReturnSuccess ? 0 : (int)r;
}

static CFDictionaryRef matchDict(int vendor, int page) {
    CFMutableDictionaryRef d = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFNumberRef v = CFNumberCreate(NULL, kCFNumberIntType, &vendor);
    CFNumberRef p = CFNumberCreate(NULL, kCFNumberIntType, &page);
    CFDictionarySetValue(d, CFSTR(kIOHIDVendorIDKey), v);
    CFDictionarySetValue(d, CFSTR(kIOHIDPrimaryUsagePageKey), p);
    CFRelease(v);
    CFRelease(p);
    return d;
}

void macStartHID(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        hidManager = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
        CFDictionaryRef a = matchDict(0x046D, 0xFF00), b = matchDict(0x046D, 0xFF43);
        CFDictionaryRef arr[2] = {a, b};
        CFArrayRef list = CFArrayCreate(NULL, (const void **)arr, 2, &kCFTypeArrayCallBacks);
        IOHIDManagerSetDeviceMatchingMultiple(hidManager, list);
        CFRelease(list);
        CFRelease(a);
        CFRelease(b);
        IOHIDManagerRegisterDeviceMatchingCallback(hidManager, hidAdded, NULL);
        IOHIDManagerRegisterDeviceRemovalCallback(hidManager, hidRemoved, NULL);
        IOHIDManagerScheduleWithRunLoop(hidManager, CFRunLoopGetMain(), kCFRunLoopDefaultMode);
        IOHIDManagerOpen(hidManager, kIOHIDOptionsTypeNone);
    });
}

// ---------------------------------------------------------------- synthetic input

static CGEventSourceRef source(void) {
    static CGEventSourceRef s;
    if (!s) s = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    return s;
}

static void post(CGEventRef e) {
    CGEventSetIntegerValueField(e, kCGEventSourceUserData, MISHK_MAGIC);
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

void macKey(int keycode, int down, uint64_t flags) {
    CGEventRef e = CGEventCreateKeyboardEvent(source(), (CGKeyCode)keycode, down != 0);
    CGEventSetFlags(e, (CGEventFlags)flags);
    post(e);
}

void macMediaKey(int key, int down) {
    // NX_KEYTYPE_* media keys are "system defined" events.
    NSEvent *ev = [NSEvent otherEventWithType:NSEventTypeSystemDefined location:NSZeroPoint
        modifierFlags:(down ? 0xa00 : 0xb00) timestamp:0 windowNumber:0 context:nil subtype:8
        data1:((key << 16) | ((down ? 0xa : 0xb) << 8)) data2:-1];
    CGEventRef e = CGEventCreateCopy([ev CGEvent]);
    post(e);
}

void macMouseButton(int button, int down) {
    CGEventRef here = CGEventCreate(NULL);
    CGPoint pt = CGEventGetLocation(here);
    CFRelease(here);
    CGEventType type;
    CGMouseButton b;
    switch (button) {
    case 0: type = down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp; b = kCGMouseButtonLeft; break;
    case 1: type = down ? kCGEventRightMouseDown : kCGEventRightMouseUp; b = kCGMouseButtonRight; break;
    default: type = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp; b = (CGMouseButton)button; break;
    }
    CGEventRef e = CGEventCreateMouseEvent(source(), type, pt, b);
    CGEventSetIntegerValueField(e, kCGMouseEventButtonNumber, button);
    post(e);
}

void macScroll(int dy, int dx) {
    CGEventRef e = CGEventCreateScrollWheelEvent(source(), kCGScrollEventUnitPixel, 2, dy, dx);
    post(e);
}

static char *copyString(NSString *s) { return strdup(s ? s.UTF8String : ""); }

char *macFrontApp(void) {
    NSRunningApplication *a = [[NSWorkspace sharedWorkspace] frontmostApplication];
    NSString *name = a.executableURL.lastPathComponent ?: a.localizedName;
    return copyString(name);
}

char *macRunningApps(void) {
    NSMutableArray *out = [NSMutableArray array];
    NSMutableSet *seen = [NSMutableSet set];
    NSString *me = [[NSRunningApplication currentApplication] bundleIdentifier];
    for (NSRunningApplication *a in [[NSWorkspace sharedWorkspace] runningApplications]) {
        if (a.activationPolicy != NSApplicationActivationPolicyRegular) continue;
        if (me && [a.bundleIdentifier isEqualToString:me]) continue;
        NSString *exe = a.executableURL.lastPathComponent;
        if (!exe || [seen containsObject:exe]) continue;
        [seen addObject:exe];
        [out addObject:@{@"exe": exe.lowercaseString, @"title": a.localizedName ?: exe}];
    }
    NSData *json = [NSJSONSerialization dataWithJSONObject:out options:0 error:nil];
    return copyString([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding]);
}

// ---------------------------------------------------------------- any mouse: event tap

static CFMachPortRef tap;
static CFRunLoopSourceRef tapSource;

static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef e, void *ctx) {
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (tap) CGEventTapEnable(tap, true);
        return e;
    }
    if (CGEventGetIntegerValueField(e, kCGEventSourceUserData) == MISHK_MAGIC) return e;
    if (type == kCGEventOtherMouseDragged || type == kCGEventMouseMoved) {
        goTapMove((int)CGEventGetIntegerValueField(e, kCGMouseEventDeltaX), (int)CGEventGetIntegerValueField(e, kCGMouseEventDeltaY));
        return e;
    }
    if (type != kCGEventOtherMouseDown && type != kCGEventOtherMouseUp) return e;
    int b = (int)CGEventGetIntegerValueField(e, kCGMouseEventButtonNumber);
    if (goTapButton(b, type == kCGEventOtherMouseDown)) return NULL;
    return e;
}

int macAccessibilityTrusted(int prompt) {
    NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @(prompt != 0)};
    return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts) ? 1 : 0;
}

int macSetTap(int on) {
    if (on && !tap) {
        CGEventMask mask = CGEventMaskBit(kCGEventOtherMouseDown) | CGEventMaskBit(kCGEventOtherMouseUp) |
                           CGEventMaskBit(kCGEventOtherMouseDragged) | CGEventMaskBit(kCGEventMouseMoved);
        tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault, mask, tapCallback, NULL);
        if (!tap) return 0; // needs Accessibility permission
        tapSource = CFMachPortCreateRunLoopSource(NULL, tap, 0);
        CFRunLoopAddSource(CFRunLoopGetMain(), tapSource, kCFRunLoopCommonModes);
        CGEventTapEnable(tap, true);
    } else if (!on && tap) {
        CGEventTapEnable(tap, false);
        CFRunLoopRemoveSource(CFRunLoopGetMain(), tapSource, kCFRunLoopCommonModes);
        CFRelease(tapSource);
        CFRelease(tap);
        tap = NULL;
        tapSource = NULL;
    }
    return 1;
}
