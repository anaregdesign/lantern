import unittest
from unittest.mock import Mock

from deadline import Window, watch_deadlines


class DeadlineTest(unittest.TestCase):
    def setUp(self):
        self.clock = {"monotonic": 0, "boot": 0}
        self.window = Window(lambda: self.clock["monotonic"], lambda: self.clock["boot"])

    def test_suspend_expires_work_and_hard_deadline_with_active_clock_fixed(self):
        work, hard = self.window.limit(89), self.window.limit(120)
        self.clock.update(monotonic=30_000_000_000, boot=130_000_000_000)
        for limit in (work, hard):
            with self.assertRaises(TimeoutError):
                limit.check()
        self.assertEqual(max(self.window.elapsed()), 130_000_000_000)

    def test_active_clock_also_limits_and_cap_never_extends_either_clock(self):
        hard = self.window.limit(120)
        self.clock.update(monotonic=119_000_000_000, boot=118_000_000_000)
        capped = hard.cap(5)
        self.assertEqual(capped.check(), 1)
        self.clock["monotonic"] = 121_000_000_000
        with self.assertRaises(TimeoutError):
            capped.check()

    def test_resume_past_all_stops_runs_terminal_actions_without_long_wait(self):
        waits, actions = [], []
        finished = Mock()
        finished.is_set.return_value = False

        def suspended_wait(seconds):
            waits.append(seconds)
            self.clock.update(monotonic=30_000_000_000, boot=130_000_000_000)
            return False

        finished.wait.side_effect = suspended_wait
        watch_deadlines(finished, self.window, ((89, lambda: actions.append("stop")),
                                              (105, lambda: actions.append("terminate")),
                                              (115, lambda: actions.append("kill"))))
        self.assertEqual(actions, ["stop", "terminate", "kill"])
        self.assertEqual(len(waits), 1)
        self.assertLessEqual(waits[0], .05)

    def test_finished_watchdog_starts_no_action(self):
        finished, action = Mock(), Mock()
        finished.wait.return_value = True
        watch_deadlines(finished, self.window, ((89, action),))
        action.assert_not_called()
