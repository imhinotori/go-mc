# recursive.star — a self-recursive fn called at module load. Recursion is OFF
# (FileOptions.Recursion is the zero value false => the guard is ON), so this is
# a dynamic error: "function f called recursively".
def f(n):
    return f(n - 1)

f(5)
