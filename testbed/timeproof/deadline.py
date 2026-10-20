"""Execution limits bounded by both active time and suspend-inclusive time."""

import time


class Window:
    def __init__(self, monotonic=None, boottime=None):
        self.monotonic = monotonic or time.monotonic_ns
        self.boottime = boottime or (lambda: time.clock_gettime_ns(time.CLOCK_BOOTTIME))
        self.started = self.monotonic()
        self.boot = self.boottime()

    def limit(self, seconds):
        offset = int(seconds * 1e9)
        return Deadline(self, self.started + offset, self.boot + offset)

    def elapsed(self):
        return self.monotonic() - self.started, self.boottime() - self.boot


class Deadline:
    def __init__(self, window, monotonic_end, boot_end):
        self.window, self.monotonic_end, self.boot_end = window, monotonic_end, boot_end

    def remaining(self):
        return min(self.monotonic_end - self.window.monotonic(),
                   self.boot_end - self.window.boottime()) / 1e9

    def check(self):
        remaining = self.remaining()
        if remaining <= 0:
            raise TimeoutError("execution deadline")
        return remaining

    def cap(self, seconds):
        offset = int(seconds * 1e9)
        return Deadline(self.window, min(self.monotonic_end, self.window.monotonic() + offset),
                        min(self.boot_end, self.window.boottime() + offset))

    def pause(self, seconds):
        local = self.cap(seconds)
        while (remaining := local.remaining()) > 0:
            time.sleep(min(.05, remaining))
        self.check()


def watch_deadlines(finished, window, actions):
    for offset, action in actions:
        limit = window.limit(offset)
        while (remaining := limit.remaining()) > 0:
            if finished.wait(min(.05, remaining)):
                return
        if finished.is_set():
            return
        action()
