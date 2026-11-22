package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; host_call_fn call; host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*plugin_free_fn)(void*, size_t);
typedef void (*plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; plugin_call_fn call; plugin_free_fn free_buffer; plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static int call_host_api(const char* method, const uint8_t* request, size_t len, cliproxy_buffer* response) {
    if (!stored_host || !stored_host->call) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, len, response);
}
static void free_host_buffer(void* ptr, size_t len) { if (stored_host && stored_host->free_buffer && ptr) stored_host->free_buffer(ptr, len); }
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || uint32(host.abi_version) != pluginabi.ABIVersion {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) (rc C.int) {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	defer func() {
		if recover() != nil {
			writeResponse(response, envelope(false, pluginabi.NewError("plugin_panic", "plugin call failed", 500)))
			rc = 1
		}
	}()
	var input []byte
	if request != nil && requestLen > 0 {
		input = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	result, err := handleMethod(C.GoString(method), input)
	if err != nil {
		p := &pluginabi.Error{}
		if !errors.As(err, &p) {
			p = pluginabi.NewError("plugin_error", err.Error(), 500)
		}
		writeResponse(response, envelope(false, p))
		return 1
	}
	raw, _ := json.Marshal(result)
	writeResponse(response, envelope(true, nil, raw))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, size C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func writeResponse(out *C.cliproxy_buffer, raw []byte) {
	if len(raw) == 0 || out == nil {
		return
	}
	out.ptr = C.CBytes(raw)
	out.len = C.size_t(len(raw))
}
func envelope(ok bool, err *pluginabi.Error, result ...[]byte) []byte {
	e := pluginabi.Envelope{OK: ok, Error: err}
	if ok && len(result) > 0 {
		e.Result = result[0]
	}
	b, _ := json.Marshal(e)
	return b
}

func hostRPC(method string, payload any, result any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	cp := C.CBytes(raw)
	defer C.free(cp)
	var out C.cliproxy_buffer
	rc := C.call_host_api(cm, (*C.uint8_t)(cp), C.size_t(len(raw)), &out)
	if out.ptr != nil {
		defer C.free_host_buffer(out.ptr, out.len)
	}
	if rc != 0 || out.ptr == nil || out.len == 0 {
		return fmt.Errorf("host callback failed")
	}
	var env pluginabi.Envelope
	if json.Unmarshal(C.GoBytes(out.ptr, C.int(out.len)), &env) != nil {
		return fmt.Errorf("invalid host response")
	}
	if !env.OK || rc != 0 {
		if env.Error != nil {
			return env.Error
		}
		return fmt.Errorf("host callback failed")
	}
	if result != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, result)
	}
	return nil
}
