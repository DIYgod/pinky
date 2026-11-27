#import <UIKit/UIKit.h>
#import <dlfcn.h>

static void RunXCTest(void) {
    // Use the XCTest shipped with the mounted developer disk image.
    void *library = dlopen("/Developer/Library/Frameworks/XCTest.framework/XCTest", RTLD_NOW | RTLD_GLOBAL);
    if (!library) {
        NSLog(@"Pinky Runner could not load XCTest: %s", dlerror());
        return;
    }
    void (*run)(const void *) = (void (*)(const void *))dlsym(library, "_XCTestMain");
    if (!run) {
        NSLog(@"Pinky Runner could not find XCTest's entry point: %s", dlerror());
        return;
    }
    // XCTest reads the configuration supplied by the testmanagerd session.
    run(NULL);
}

@interface PinkyRunner : UIResponder <UIApplicationDelegate>
@property (strong, nonatomic) UIWindow *window;
@end

@implementation PinkyRunner
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)options {
    self.window = [[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds];
    UIViewController *controller = [[UIViewController alloc] init];
    self.window.rootViewController = controller;
    UILabel *label = [[UILabel alloc] initWithFrame:controller.view.bounds];
    label.textAlignment = NSTextAlignmentCenter;
    label.text = @"Pinky";
    label.autoresizingMask = UIViewAutoresizingFlexibleWidth | UIViewAutoresizingFlexibleHeight;
    controller.view.backgroundColor = UIColor.systemBackgroundColor;
    [controller.view addSubview:label];
    [self.window makeKeyAndVisible];
    if (getenv("XCTestSessionIdentifier") != NULL) {
        // XCTest runs its own main run loop. Let the main queue stay free
        // so WDA can service requests there.
        CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopDefaultMode, ^{ RunXCTest(); });
        CFRunLoopWakeUp(CFRunLoopGetMain());
    }
    return YES;
}
@end

int main(int argc, char *argv[]) {
    @autoreleasepool {
        return UIApplicationMain(argc, argv, nil, NSStringFromClass(PinkyRunner.class));
    }
}
