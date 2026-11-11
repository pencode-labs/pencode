#import <AppKit/AppKit.h>

extern void pencode_open_document(const char *path);

@interface PencodeDocumentEvents : NSObject
- (void)openDocuments:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply;
@end

@implementation PencodeDocumentEvents
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
        // Initialize AppKit's handlers first, then override only open-document
        // delivery. The engine continues to own the application delegate.
        [NSApplication sharedApplication];
        static PencodeDocumentEvents *handler;
        if (handler == nil) handler = [[PencodeDocumentEvents alloc] init];
        [[NSAppleEventManager sharedAppleEventManager] setEventHandler:handler
            andSelector:@selector(openDocuments:withReplyEvent:)
            forEventClass:kCoreEventClass andEventID:kAEOpenDocuments];
    }
}
