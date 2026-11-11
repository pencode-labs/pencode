#import <AppKit/AppKit.h>
#include <assert.h>
#include <string.h>

extern void pencode_configure_document_events(void);

static NSUInteger received;
static const char *expected[] = {"/tmp/中文 空格.txt", "/tmp/--second.go", "/tmp/third.md"};

void pencode_open_document(const char *path) {
    assert(received < 3);
    assert(strcmp(path, expected[received++]) == 0);
}

static void sendFiles(NSUInteger first, NSUInteger count) {
    NSAppleEventDescriptor *event = [NSAppleEventDescriptor
        appleEventWithEventClass:kCoreEventClass eventID:kAEOpenDocuments
        targetDescriptor:[NSAppleEventDescriptor currentProcessDescriptor]
        returnID:kAutoGenerateReturnID transactionID:kAnyTransactionID];
    NSAppleEventDescriptor *files = [NSAppleEventDescriptor listDescriptor];
    for (NSUInteger i = 0; i < count; i++) {
        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:expected[first+i]]];
        [files insertDescriptor:[NSAppleEventDescriptor descriptorWithFileURL:url] atIndex:i+1];
    }
    [event setParamDescriptor:files forKeyword:keyDirectObject];
    AppleEvent reply = {typeNull, NULL};
    OSErr error = [[NSAppleEventManager sharedAppleEventManager]
        dispatchRawAppleEvent:event.aeDesc withRawReply:&reply handlerRefCon:0];
    assert(error == noErr);
    AEDisposeDesc(&reply);
}

int main(void) {
    @autoreleasepool {
        pencode_configure_document_events();
        // Match the engine's initialization order: the document handler is
        // configured before it installs its own application delegate.
        NSObject *delegate = [[NSObject alloc] init];
        NSApp.delegate = (id<NSApplicationDelegate>)delegate;
        sendFiles(0, 2);
        assert(received == 2);
        [NSApp finishLaunching];
        assert(NSApp.delegate == delegate);
        sendFiles(2, 1);
        assert(received == 3);
        sendFiles(0, 0);
        assert(received == 3);
        NSApp.delegate = nil;
        [delegate release];
    }
    return 0;
}
