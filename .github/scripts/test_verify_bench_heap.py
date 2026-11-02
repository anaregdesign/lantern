import json
from pathlib import Path
import unittest

import verify_bench_heap as gate
from local_gate_plan import affected_steps, steps


class BenchHeapVerificationTest(unittest.TestCase):
    def test_observer_targets_have_real_tests_after_runtime_extraction(self):
        self.assertIn("./service", gate.TARGETS)
        self.assertIn("./internal/dataruntime", gate.TARGETS)
        root = Path(__file__).resolve().parents[2] / "server"
        for target in gate.TARGETS:
            with self.subTest(target=target):
                self.assertTrue(any("func TestBenchHeap" in source.read_text()
                                    for source in (root / target).glob("*_test.go")))

    def events(self):
        result = []
        for target in gate.TARGETS:
            package = gate.MODULE + target[1:]
            for action in ("run", "pass"):
                result.append(dict(Package=package, Action=action, Test="TestBenchHeapBoundary"))
            result.append(dict(Package=package, Action="pass"))
        return result

    def test_commands_pin_tag_race_targets_and_dependency_boundary(self):
        normal, tagged, tests, controlled = [command for _, command in gate.commands()]
        self.assertEqual(normal, ("go", "list", "-mod=readonly", "-deps", "./cmd"))
        self.assertEqual(tagged, ("go", "list", "-mod=readonly", "-tags=lantern_bench_heap", "-deps", "./cmd"))
        self.assertEqual(tests, ("go", "test", "-mod=readonly", "-race", "-count=1", "-json",
                                 "-tags=lantern_bench_heap", *gate.TARGETS, "-run", "^TestBenchHeap"))
        self.assertEqual(controlled, ("go", "test", "-mod=readonly", "-race", "-count=1", "-json",
                                      "-tags=lantern_bench_heap,lantern_test_time", "./cmd", "-run",
                                      "^TestBenchHeapRefusalSurvivesCancellation$"))
        normal_deps = gate.MODULE + "/cmd\nnet/http\n"
        gate.require_dependencies(normal_deps, False)
        gate.require_dependencies(normal_deps + gate.OBSERVER + "\n", True)
        for text, tagged_mode in (("", False), (normal_deps, True),
                                  (normal_deps + gate.OBSERVER + "\n", False)):
            with self.subTest(text=text, tagged=tagged_mode), self.assertRaises(ValueError):
                gate.require_dependencies(text, tagged_mode)

    def test_each_target_requires_nonempty_completed_unskipped_tests(self):
        events = self.events()
        encode = lambda values: "\n".join(json.dumps(event) for event in values)
        self.assertEqual(len(gate.require_tests(encode(events))), len(gate.TARGETS))
        failures = [[], events[3:], events[:-1], events + [events[0]],
                    events + [dict(Package=gate.MODULE + "/cmd", Action="fail")],
                    events + [dict(Package=gate.MODULE + "/cmd", Action="skip", Test="TestBenchHeapSkipped")],
                    events + [dict(Package=gate.MODULE + "/other", Action="pass")],
                    events + [dict(Package=gate.MODULE + "/cmd", Action="run", Test="TestOther")]]
        failures.append([{**event, **({"Test": event["Test"] + "/only_subtest"} if "Test" in event else {})}
                         for event in events])
        for values in failures:
            with self.subTest(values=values), self.assertRaises(ValueError):
                gate.require_tests(encode(values))

    def test_controlled_cmd_requires_the_exact_refusal_regression(self):
        package = gate.MODULE + "/cmd"
        name = gate.CONTROLLED_TEST
        required = ((package, name),)
        events = [dict(Package=package, Action="run", Test=name),
                  dict(Package=package, Action="run", Test=name + "/refusal"),
                  dict(Package=package, Action="pass", Test=name + "/refusal"),
                  dict(Package=package, Action="pass", Test=name),
                  dict(Package=package, Action="pass")]
        encode = lambda values: "\n".join(json.dumps(event) for event in values)
        self.assertIn(name, gate.require_tests(encode(events), ("./cmd",), required)[package])
        failures = [[], events[:-1], events[1:],
                    [{**event, **({"Test": "TestBenchHeapAtomicNoReplace"} if "Test" in event else {})}
                     for event in events if not event.get("Test", "").endswith("/refusal")],
                    events + [dict(Package=package, Action="skip", Test=name)],
                    events + [dict(Package=package, Action="fail")]]
        for values in failures:
            with self.subTest(values=values), self.assertRaises(ValueError):
                gate.require_tests(encode(values), ("./cmd",), required)

    def test_local_and_hosted_gates_require_the_same_observer_command(self):
        plan = steps(Path("/tmp/evidence"))
        step = next(item for item in plan if item.name == "server-bench-heap-tests")
        self.assertEqual(step.cwd, ".")
        self.assertEqual(step.command, ("python3", "-B", ".github/scripts/verify_bench_heap.py"))
        self.assertEqual(len(plan), 79)
        for modules, selected in ((["server", "."], True), (["core", "."], False)):
            scope = dict(full=False, families=[], go_modules=modules, generation=False)
            self.assertEqual(step in affected_steps(plan, scope), selected)
        root = Path(__file__).resolve().parents[1]
        for workflow in ("go.yml", "_verify.yml"):
            text = (root / "workflows" / workflow).read_text()
            self.assertIn("run: python3 -B .github/scripts/verify_bench_heap.py", text)
            self.assertNotIn("continue-on-error", text)
        self.assertIn("timeout-minutes: 15", (root / "workflows/_verify.yml").read_text())


if __name__ == "__main__":
    unittest.main()
