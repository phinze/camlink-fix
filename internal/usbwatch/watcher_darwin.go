package usbwatch

import (
	"context"
	"log"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CoreFoundation types
type (
	cfAllocatorRef   uintptr
	cfDictionaryRef  uintptr
	cfIndex          int64
	cfMutableDictRef uintptr
	cfNumberRef      uintptr
	cfNumberType     = cfIndex
	cfRunLoopRef     uintptr
	cfRunLoopSourceRef uintptr
	cfStringRef      uintptr
	cfTypeRef        uintptr

	cfStringEncoding uint32
)

// IOKit types
type (
	ioIteratorT     uint32
	ioObjectT       uint32
	ioOptionBits    uint32
	ioReturn        int32
	machPortT       uint32
)

// IOKit notification port (opaque struct pointer)
type ioNotificationPortRef uintptr

const (
	kCFAllocatorDefault   cfAllocatorRef   = 0
	kCFNumberSInt32Type   cfIndex          = 3
	kCFStringEncodingUTF8 cfStringEncoding = 0x08000100

	kIOReturnSuccess ioReturn     = 0
	kIOFirstMatch    ioOptionBits = 0x00000001

	kIOMasterPortDefault machPortT = 0
)

// purego function bindings — CoreFoundation
var (
	cfDictionaryCreateMutable func(allocator cfAllocatorRef, capacity cfIndex, keyCallBacks uintptr, valueCallBacks uintptr) cfMutableDictRef
	cfDictionarySetValue      func(dict cfMutableDictRef, key unsafe.Pointer, value unsafe.Pointer)
	cfNumberCreate            func(allocator cfAllocatorRef, theType cfNumberType, valuePtr unsafe.Pointer) cfNumberRef
	cfRelease                 func(cf cfTypeRef)
	cfRunLoopAddSource        func(rl cfRunLoopRef, source cfRunLoopSourceRef, mode uintptr)
	cfRunLoopGetCurrent       func() cfRunLoopRef
	cfRunLoopRun              func()
	cfRunLoopStop             func(runLoop cfRunLoopRef)
	cfStringCreateWithBytes   func(alloc cfAllocatorRef, bytes []byte, numBytes cfIndex, encoding cfStringEncoding, isExternalRepresentation bool) cfStringRef
)

// purego function bindings — IOKit
var (
	ioIteratorNext                    func(iterator ioIteratorT) ioObjectT
	ioNotificationPortCreate          func(masterPort machPortT) ioNotificationPortRef
	ioNotificationPortGetRunLoopSource func(notify ioNotificationPortRef) cfRunLoopSourceRef
	ioNotificationPortDestroy         func(notify ioNotificationPortRef)
	ioObjectRelease                   func(object ioObjectT) ioReturn
	ioServiceAddMatchingNotification  func(notifyPort ioNotificationPortRef, notificationType uintptr, matching cfMutableDictRef, callback uintptr, refCon unsafe.Pointer, notification *ioIteratorT) ioReturn
	ioServiceMatching                 func(name []byte) cfMutableDictRef
)

// Global pointers needed by purego
var kCFRunLoopDefaultMode uintptr
var kCFTypeDictionaryKeyCallBacks uintptr
var kCFTypeDictionaryValueCallBacks uintptr

// IOKit notification type string
var kIOMatchedNotification uintptr

func init() {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&cfDictionaryCreateMutable, cf, "CFDictionaryCreateMutable")
	purego.RegisterLibFunc(&cfDictionarySetValue, cf, "CFDictionarySetValue")
	purego.RegisterLibFunc(&cfNumberCreate, cf, "CFNumberCreate")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfRunLoopAddSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopRun, cf, "CFRunLoopRun")
	purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
	purego.RegisterLibFunc(&cfStringCreateWithBytes, cf, "CFStringCreateWithBytes")

	kCFRunLoopDefaultMode, err = purego.Dlsym(cf, "kCFRunLoopDefaultMode")
	if err != nil {
		panic(err)
	}
	kCFTypeDictionaryKeyCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	if err != nil {
		panic(err)
	}
	kCFTypeDictionaryValueCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if err != nil {
		panic(err)
	}

	iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&ioIteratorNext, iokit, "IOIteratorNext")
	purego.RegisterLibFunc(&ioNotificationPortCreate, iokit, "IONotificationPortCreate")
	purego.RegisterLibFunc(&ioNotificationPortGetRunLoopSource, iokit, "IONotificationPortGetRunLoopSource")
	purego.RegisterLibFunc(&ioNotificationPortDestroy, iokit, "IONotificationPortDestroy")
	purego.RegisterLibFunc(&ioObjectRelease, iokit, "IOObjectRelease")
	purego.RegisterLibFunc(&ioServiceAddMatchingNotification, iokit, "IOServiceAddMatchingNotification")
	purego.RegisterLibFunc(&ioServiceMatching, iokit, "IOServiceMatching")

	kIOMatchedNotification, err = purego.Dlsym(iokit, "kIOMatchedNotification")
	if err != nil {
		panic(err)
	}
}

// callbackCtx holds the state passed to the IOKit callback.
var callbackCtx *watcherCtx

type watcherCtx struct {
	ch chan<- struct{}
}

// drainIterator must be called each time the notification fires (and on
// initial setup) or IOKit will stop delivering notifications.
func drainIterator(iterator ioIteratorT) int {
	count := 0
	for {
		obj := ioIteratorNext(iterator)
		if obj == 0 {
			break
		}
		ioObjectRelease(obj)
		count++
	}
	return count
}

func matchCallback(_ unsafe.Pointer, iterator ioIteratorT) {
	n := drainIterator(iterator)
	if n > 0 && callbackCtx != nil {
		log.Printf("usbwatch: USB device arrived (%d matched)", n)
		select {
		case callbackCtx.ch <- struct{}{}:
		default:
		}
	}
}

var matchCallbackPtr = purego.NewCallback(matchCallback)

func cfStr(s string) cfStringRef {
	b := []byte(s)
	return cfStringCreateWithBytes(kCFAllocatorDefault, b, cfIndex(len(b)), kCFStringEncodingUTF8, false)
}

func cfInt32(v int32) cfNumberRef {
	return cfNumberCreate(kCFAllocatorDefault, kCFNumberSInt32Type, unsafe.Pointer(&v))
}

// Watch returns a channel that receives a signal each time a USB device
// matching the given vendor and product IDs appears on the bus. Uses IOKit's
// IOServiceAddMatchingNotification for zero-CPU-cost waiting.
// The watcher stops when ctx is cancelled.
func Watch(ctx context.Context, vendorID, productID int32) <-chan struct{} {
	ch := make(chan struct{}, 1)
	callbackCtx = &watcherCtx{ch: ch}

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		notifyPort := ioNotificationPortCreate(kIOMasterPortDefault)
		if notifyPort == 0 {
			log.Println("usbwatch: failed to create IONotificationPort")
			return
		}

		// Create matching dictionary for IOUSBHostDevice with vendor/product filter
		matching := ioServiceMatching(append([]byte("IOUSBHostDevice"), 0))
		if matching == 0 {
			log.Println("usbwatch: IOServiceMatching returned nil")
			ioNotificationPortDestroy(notifyPort)
			return
		}

		// Add vendor and product ID to the matching dictionary
		vidKey := cfStr("idVendor")
		pidKey := cfStr("idProduct")
		vidVal := cfInt32(vendorID)
		pidVal := cfInt32(productID)

		cfDictionarySetValue(cfMutableDictRef(matching), unsafe.Pointer(vidKey), unsafe.Pointer(vidVal))
		cfDictionarySetValue(cfMutableDictRef(matching), unsafe.Pointer(pidKey), unsafe.Pointer(pidVal))

		cfRelease(cfTypeRef(vidKey))
		cfRelease(cfTypeRef(pidKey))
		cfRelease(cfTypeRef(vidVal))
		cfRelease(cfTypeRef(pidVal))

		// Register for matching notifications
		// NOTE: matching dictionary is consumed by this call — do not release it
		var iterator ioIteratorT
		kr := ioServiceAddMatchingNotification(
			notifyPort,
			kIOMatchedNotification,
			cfMutableDictRef(matching),
			matchCallbackPtr,
			nil,
			&iterator,
		)
		if kr != kIOReturnSuccess {
			log.Printf("usbwatch: IOServiceAddMatchingNotification failed: 0x%08x", kr)
			ioNotificationPortDestroy(notifyPort)
			return
		}

		// Drain the iterator to arm the notification (IOKit requirement)
		n := drainIterator(iterator)
		if n > 0 {
			log.Printf("usbwatch: %d device(s) already present at startup", n)
		}

		// Wire notification port into the current thread's run loop
		rl := cfRunLoopGetCurrent()
		source := ioNotificationPortGetRunLoopSource(notifyPort)
		cfRunLoopAddSource(rl, source, *(*uintptr)(unsafe.Pointer(&kCFRunLoopDefaultMode)))

		// Stop the run loop when context is cancelled
		go func() {
			<-ctx.Done()
			cfRunLoopStop(rl)
		}()

		log.Printf("usbwatch: listening for USB device arrivals (vendor=0x%04x product=0x%04x)", vendorID, productID)
		cfRunLoopRun()

		ioNotificationPortDestroy(notifyPort)
		callbackCtx = nil
		log.Println("usbwatch: stopped")
	}()

	return ch
}
