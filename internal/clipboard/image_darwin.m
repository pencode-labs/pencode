#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

int pencode_clipboard_image(void **data, int *size) {
    @autoreleasepool {
        NSPasteboard *pasteboard = [NSPasteboard generalPasteboard];
        NSData *png = [pasteboard dataForType:NSPasteboardTypePNG];
        if (!png) {
            NSData *tiff = [pasteboard dataForType:NSPasteboardTypeTIFF];
            if (!tiff) return 0;
            if (tiff.length > (64 << 20)) return 2;
            NSBitmapImageRep *image = [NSBitmapImageRep imageRepWithData:tiff];
            if (!image) return -1;
            if (image.pixelsWide <= 0 || image.pixelsHigh <= 0 ||
                image.pixelsWide > (64 << 20) / 4 / image.pixelsHigh) return 2;
            png = [image representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
            if (!png) return -1;
        }
        if (png.length > (64 << 20)) return 2;
        if (!png.length) return 0;
        *data = malloc(png.length);
        if (!*data) return -1;
        *size = (int)png.length;
        memcpy(*data, png.bytes, png.length);
        return 1;
    }
}
