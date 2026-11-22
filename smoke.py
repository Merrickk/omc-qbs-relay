import ctypes, json, sys

lib_path = sys.argv[1]
version = sys.argv[2]
lib = ctypes.CDLL(lib_path)
for sym in ['cliproxy_plugin_init','cliproxyPluginCall','cliproxyPluginFree','cliproxyPluginShutdown']:
    if not hasattr(lib, sym):
        raise SystemExit(f'missing symbol: {sym}')

class Buf(ctypes.Structure):
    _fields_ = [('ptr', ctypes.c_void_p), ('len', ctypes.c_size_t)]

lib.cliproxyPluginCall.argtypes = [ctypes.c_char_p, ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t, ctypes.POINTER(Buf)]
lib.cliproxyPluginCall.restype = ctypes.c_int
lib.cliproxyPluginFree.argtypes = [ctypes.c_void_p, ctypes.c_size_t]

def call(method, payload):
    raw = json.dumps(payload).encode()
    arr = (ctypes.c_uint8 * len(raw)).from_buffer_copy(raw)
    out = Buf()
    rc = lib.cliproxyPluginCall(method.encode(), arr, len(raw), ctypes.byref(out))
    body = ctypes.string_at(out.ptr, out.len)
    if out.ptr: lib.cliproxyPluginFree(out.ptr, out.len)
    env = json.loads(body)
    if rc != 0 or not env.get('ok'):
        raise SystemExit(f'{method} failed: rc={rc} env={env}')
    return env.get('result')

reg = call('plugin.register', {'config_yaml': ''})
meta = reg['metadata']
assert meta['Version'] == version, meta
assert meta['Name'] == 'AWS Quick', meta
print(f'ABI/resource verified: {lib_path}, version={version}, name={meta["Name"]}')
