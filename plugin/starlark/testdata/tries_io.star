# tries_io.star — an I/O attempt at module load. There is no fs/network builtin
# in safeGlobals or the Starlark universe, so `open` is "undefined: open".
open("etc/passwd")
