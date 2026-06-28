# greet.star — happy path: a module global computed at load + a fn that calls
# the registered server-neutral Go builtin `echo`.
result = echo("hi")

def greet(who):
    return echo(who)
