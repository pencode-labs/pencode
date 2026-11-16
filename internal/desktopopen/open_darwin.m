#import <AppKit/AppKit.h>

extern void pencode_open_document(const char *path);

@interface PencodeDocumentEvents : NSObject
- (void)configure:(NSNotification *)notification;
- (void)openDocuments:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply;
@end

@implementation PencodeDocumentEvents
- (void)configure:(NSNotification *)notification {
    [[NSAppleEventManager sharedAppleEventManager] setEventHandler:self
        andSelector:@selector(openDocuments:withReplyEvent:)
        forEventClass:kCoreEventClass andEventID:kAEOpenDocuments];
}

- (void)openDocuments:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply {
    NSAppleEventDescriptor *files = [event paramDescriptorForKeyword:keyDirectObject];
    for (NSInteger i = 1; i <= files.numberOfItems; i++) {
        NSURL *url = [files descriptorAtIndex:i].fileURLValue;
        if (url.isFileURL) pencode_open_document(url.path.fileSystemRepresentation);
    }
    [NSApp unhide:nil];
    for (NSWindow *window in NSApp.windows) {
        if (window.isMiniaturized) [window deminiaturize:nil];
        [window makeKeyAndOrderFront:nil];
        break;
    }
}
@end

void pencode_configure_document_events(void) {
    @autoreleasepool {
        // AppKit installs its document handler during launch. Register after
        // that setup and before initial events without owning its delegate.
        [NSApplication sharedApplication];
        static PencodeDocumentEvents *handler;
        if (handler == nil) {
            handler = [[PencodeDocumentEvents alloc] init];
            [[NSNotificationCenter defaultCenter] addObserver:handler
                selector:@selector(configure:)
                name:NSApplicationWillFinishLaunchingNotification object:NSApp];
        }
        [handler configure:nil];
    }
}
