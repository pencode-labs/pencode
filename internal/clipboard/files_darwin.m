#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

int pencode_clipboard_has_files(void) {
    @autoreleasepool {
        return [[NSPasteboard generalPasteboard] canReadObjectForClasses:@[[NSURL class]]
            options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
    }
}

char *pencode_clipboard_files(void) {
    @autoreleasepool {
        NSArray *urls = [[NSPasteboard generalPasteboard] readObjectsForClasses:@[[NSURL class]]
            options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
        if (!urls.count) return NULL;
        NSMutableString *list = [NSMutableString string];
        for (NSURL *url in urls) {
            [list appendFormat:@"%@\n", url.absoluteString];
        }
        return strdup(list.UTF8String);
    }
}
