import json
import os
import tempfile
import unittest
from unittest import mock

from check_task_start import (classify, decide, edit_gate_mode, exempt_globs, glob_regex, path_scope,
                              subagent_transcript, FULL_HEADING, LITE_HEADING, SPEC_TOOL)


def user(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": text}})


def user_parts(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": text}]}})


def tool_use(name, inp=None):
    return json.dumps({"type": "assistant", "message": {"role": "assistant", "content": [
        {"type": "tool_use", "id": "t1", "name": name, "input": inp or {}}]}})


CLAUSE = "Implement Task 3.\n\n" + FULL_HEADING + "\n\nAt task start ..."


def tool_result(tool_use_id, is_error):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": tool_use_id, "is_error": is_error,
         "content": [{"type": "text", "text": "refused"}]}]}})


class ClassifyTest(unittest.TestCase):
    def test_refused_call_does_not_pass(self):
        lines = [user(CLAUSE), tool_use(SPEC_TOOL, {"goal": "g"}), tool_result("t1", True)]
        self.assertEqual(classify(lines), "errored")

    def test_scan_stops_at_the_first_successful_result(self):
        lines = iter([user(CLAUSE), tool_use(SPEC_TOOL), tool_result("t1", False), user("never read")])
        self.assertEqual(classify(lines), "pass")
        self.assertEqual(len(list(lines)), 1)

    def test_no_call_at_all_is_a_plain_block(self):
        self.assertEqual(classify([user(CLAUSE)]), "block")

    def test_legacy_call_without_id_stops_the_scan(self):
        legacy = json.dumps({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "name": SPEC_TOOL, "input": {}}]}})
        lines = iter([user(CLAUSE), legacy, user("never read")])
        self.assertEqual(classify(lines), "pass")
        self.assertEqual(len(list(lines)), 1)

    def test_successful_call_passes(self):
        lines = [user(CLAUSE), tool_use(SPEC_TOOL, {"goal": "g"}), tool_result("t1", False)]
        self.assertEqual(classify(lines), "pass")

    def test_retry_after_a_refused_call_passes(self):
        retry = json.dumps({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "id": "t2", "name": SPEC_TOOL, "input": {}}]}})
        lines = [user(CLAUSE), tool_use(SPEC_TOOL), tool_result("t1", True), retry, tool_result("t2", False)]
        self.assertEqual(classify(lines), "pass")

    def test_implementer_without_call_blocks(self):
        self.assertEqual(classify([user(CLAUSE), tool_use("Read")]), "block")

    def test_implementer_with_call_passes(self):
        self.assertEqual(classify([user(CLAUSE), tool_use("Read"), tool_use(SPEC_TOOL, {"goal": "g"})]), "pass")

    def test_lightweight_dispatch_skips(self):
        self.assertEqual(classify([user(CLAUSE + "\n" + LITE_HEADING)]), "skip")

    def test_non_implementer_skips_even_when_clause_appears_later(self):
        lines = [user("Plan the release."),
                 tool_use("Agent", {"prompt": CLAUSE})]
        self.assertEqual(classify(lines), "skip")

    def test_first_user_entry_may_be_a_text_part_list(self):
        self.assertEqual(classify([user_parts(CLAUSE)]), "block")

    def test_entries_before_first_user_are_ignored(self):
        lines = [json.dumps({"type": "attachment"}), json.dumps({"type": "system"}), user(CLAUSE)]
        self.assertEqual(classify(lines), "block")

    def test_malformed_lines_are_skipped(self):
        self.assertEqual(classify(["not json", user(CLAUSE), "{", tool_use(SPEC_TOOL)]), "pass")

    def test_empty_transcript_skips(self):
        self.assertEqual(classify([]), "skip")

    def test_call_inside_dispatch_prompt_text_does_not_count(self):
        # The clause itself names the tool; only a tool_use entry is a call.
        self.assertEqual(classify([user(CLAUSE + "\nmcp__anti-tangent__validate_task_spec")]), "block")

    def test_non_dict_message_without_call_blocks(self):
        # A truthy non-dict "message" (e.g. a bare string) must not raise;
        # it is treated as carrying no tool_use, so the gated session blocks.
        bad = json.dumps({"type": "assistant", "message": "oops"})
        self.assertEqual(classify([user(CLAUSE), bad]), "block")

    def test_non_dict_message_then_real_call_passes(self):
        bad = json.dumps({"type": "assistant", "message": "oops"})
        self.assertEqual(classify([user(CLAUSE), bad, tool_use(SPEC_TOOL)]), "pass")

    def test_first_user_entry_with_int_content_skips(self):
        # content that is neither a string nor a list must read as "", not raise.
        entry = json.dumps({"type": "user", "message": {"content": 5}})
        self.assertEqual(classify([entry]), "skip")


class SubagentTranscriptTest(unittest.TestCase):
    def test_subagent_transcript_sits_under_the_parent_stem(self):
        got = subagent_transcript({"transcript_path": "/p/s1.jsonl", "session_id": "s1", "agent_id": "a1"})
        self.assertEqual(got, "/p/s1/subagents/agent-a1.jsonl")

    def test_main_session_has_no_subagent_transcript(self):
        self.assertEqual(subagent_transcript({"transcript_path": "/p/s1.jsonl", "session_id": "s1"}), "")

    def test_agent_id_that_is_not_an_identifier_is_refused(self):
        self.assertEqual(subagent_transcript({"transcript_path": "/p/s1.jsonl", "agent_id": "../x"}), "")

    def test_agent_id_that_is_not_a_string_is_refused(self):
        self.assertEqual(subagent_transcript({"transcript_path": "/p/s1.jsonl", "agent_id": 5}), "")

    def test_transcript_path_that_is_not_a_string_is_refused(self):
        self.assertEqual(subagent_transcript({"transcript_path": 5, "agent_id": "a1"}), "")


class EditGateSwitchTest(unittest.TestCase):
    def test_unset_and_empty_are_off(self):
        self.assertEqual(edit_gate_mode(None), "off")
        self.assertEqual(edit_gate_mode(""), "off")

    def test_values_are_case_insensitive(self):
        self.assertEqual(edit_gate_mode(" ALL "), "all")
        self.assertEqual(edit_gate_mode("Subagents"), "subagents")

    def test_unknown_value_is_none(self):
        self.assertIsNone(edit_gate_mode("yes"))

    def test_exempt_default_set_and_empty(self):
        self.assertEqual(exempt_globs(None), ["docs/**/*.md"])
        self.assertEqual(exempt_globs("a/*.md::b/**"), ["a/*.md", "b/**"])
        self.assertEqual(exempt_globs(""), [])


class GlobTest(unittest.TestCase):
    def test_double_star_crosses_directories(self):
        g = glob_regex("docs/**/*.md")
        self.assertTrue(g.match("docs/x.md"))
        self.assertTrue(g.match("docs/superpowers/specs/x.md"))

    def test_single_star_stays_in_one_directory(self):
        g = glob_regex("docs/*.md")
        self.assertTrue(g.match("docs/x.md"))
        self.assertFalse(g.match("docs/a/x.md"))

    def test_glob_matches_whole_path(self):
        g = glob_regex("docs/**/*.md")
        self.assertFalse(g.match("docs/x.md.go"))
        self.assertFalse(g.match("src/docs/x.md"))


class PathScopeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self.tmp.name)
        os.makedirs(os.path.join(self.root, "docs"))
        os.makedirs(os.path.join(self.root, "src"))

    def tearDown(self):
        self.tmp.cleanup()

    def scope(self, target, globs=("docs/**/*.md",)):
        return path_scope(target, self.root, list(globs))

    def test_doc_under_docs_is_exempt(self):
        self.assertEqual(self.scope(os.path.join(self.root, "docs/specs/x.md")), "exempt")

    def test_relative_target_is_taken_against_cwd(self):
        self.assertEqual(self.scope("docs/x.md"), "exempt")
        self.assertEqual(self.scope("src/x.go"), "gated")

    def test_markdown_outside_docs_is_gated(self):
        self.assertEqual(self.scope(os.path.join(self.root, "CLAUDE.md")), "gated")

    def test_code_under_docs_is_gated(self):
        self.assertEqual(self.scope(os.path.join(self.root, "docs/x.go")), "gated")

    def test_symlink_into_src_is_gated(self):
        os.symlink(os.path.join(self.root, "src/main.go"), os.path.join(self.root, "docs/x.md"))
        self.assertEqual(self.scope(os.path.join(self.root, "docs/x.md")), "gated")

    def test_outside_cwd_is_not_gated(self):
        self.assertEqual(self.scope("/tmp/elsewhere/x.go"), "outside")
        self.assertEqual(self.scope(os.path.join(self.root, "../other/x.go")), "outside")

    def test_empty_globs_gate_docs(self):
        self.assertEqual(self.scope("docs/x.md", globs=()), "gated")

    def test_no_cwd_is_outside(self):
        self.assertEqual(path_scope("x.go", None, []), "outside")

    def test_project_root_gates_a_sibling_of_the_cwd(self):
        cwd = os.path.join(self.root, "src")
        target = os.path.join(self.root, "lib/x.go")
        self.assertEqual(path_scope(target, cwd, [], self.root), "gated")
        self.assertEqual(path_scope("../lib/x.go", cwd, [], self.root), "gated")

    def test_project_root_anchors_the_exemption_globs(self):
        cwd = os.path.join(self.root, "src")
        self.assertEqual(path_scope("../docs/x.md", cwd, ["docs/**/*.md"], self.root), "exempt")

    def test_project_root_still_leaves_other_repositories_outside(self):
        cwd = os.path.join(self.root, "src")
        self.assertEqual(path_scope("/tmp/elsewhere/x.go", cwd, [], self.root), "outside")


class DecideTest(unittest.TestCase):
    """decide() end to end over real transcript files."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self.tmp.name)
        self.parent = os.path.join(self.root, "parent.jsonl")
        self.env = mock.patch.dict(os.environ)
        self.env.start()
        os.environ.pop("CLAUDE_PROJECT_DIR", None)
        os.makedirs(os.path.join(self.root, "parent", "subagents"))

    def tearDown(self):
        self.env.stop()
        self.tmp.cleanup()

    def write(self, path, lines):
        with open(path, "w") as fh:
            fh.write("\n".join(lines) + "\n")

    def payload(self, target="src/x.go", agent=None):
        data = {"tool_name": "Edit", "tool_input": {"file_path": os.path.join(self.root, target)},
                "transcript_path": self.parent, "cwd": self.root}
        if agent:
            data["agent_id"] = agent
        return data

    def sub(self, lines):
        self.write(os.path.join(self.root, "parent", "subagents", "agent-a1.jsonl"), lines)

    def test_off_by_default_main_session_is_not_gated(self):
        self.write(self.parent, [user("fix the bug")])
        self.assertEqual(decide(self.payload(), "off", True, ["docs/**/*.md"])[:2], (3, "not-gated"))

    def test_off_by_default_plain_subagent_is_not_gated(self):
        self.sub([user("fix the bug")])
        self.assertEqual(decide(self.payload(agent="a1"), "off", True, [])[0], 3)

    def test_subagents_mode_blocks_plain_subagent(self):
        self.sub([user("fix the bug")])
        code, word, message = decide(self.payload(agent="a1"), "subagents", True, [])
        self.assertEqual((code, word), (2, "edit-gate-no-spec-call"))
        self.assertIn("ANTI_TANGENT_EDIT_GATE=subagents", message)

    def test_subagents_mode_passes_after_call(self):
        self.sub([user("fix the bug"), tool_use(SPEC_TOOL), tool_result("t1", False)])
        self.assertEqual(decide(self.payload(agent="a1"), "subagents", True, [])[0], 0)

    def test_subagents_mode_leaves_main_session_alone(self):
        self.write(self.parent, [user("fix the bug")])
        self.assertEqual(decide(self.payload(), "subagents", True, [])[0], 3)

    def test_subagents_mode_exempts_lightweight_dispatch(self):
        self.sub([user("Do it.\n" + LITE_HEADING)])
        self.assertEqual(decide(self.payload(agent="a1"), "subagents", True, [])[:2], (3, "lightweight"))

    def test_all_mode_blocks_main_session_without_call(self):
        self.write(self.parent, [user("fix the bug")])
        self.assertEqual(decide(self.payload(), "all", True, ["docs/**/*.md"])[0], 2)

    def test_all_mode_passes_main_session_after_call(self):
        self.write(self.parent, [user("fix the bug"), tool_use(SPEC_TOOL), tool_result("t1", False)])
        self.assertEqual(decide(self.payload(), "all", True, [])[0], 0)

    def test_all_mode_errored_call_blocks_with_errored_text(self):
        self.write(self.parent, [user("fix the bug"), tool_use(SPEC_TOOL), tool_result("t1", True)])
        code, word, message = decide(self.payload(), "all", True, [])
        self.assertEqual((code, word), (2, "edit-gate-spec-errored"))
        self.assertIn("tool error", message)

    def test_all_mode_pasted_envelope_does_not_pass(self):
        envelope = ("anti-tangent envelope\n  tool: validate_task_spec\n  session_id: s1\n  verdict: pass\n"
                    "mcp__anti-tangent__validate_task_spec")
        self.write(self.parent, [user("fix the bug"), user(envelope),
                                 tool_result_text("t9", envelope)])
        self.assertEqual(decide(self.payload(), "all", True, [])[0], 2)

    def test_all_mode_exempt_doc_passes_without_call(self):
        self.write(self.parent, [user("write the spec")])
        self.assertEqual(decide(self.payload("docs/specs/x.md"), "all", True, ["docs/**/*.md"])[:2], (3, "exempt"))

    def test_all_mode_outside_cwd_passes_without_call(self):
        self.write(self.parent, [user("note it")])
        data = self.payload()
        data["tool_input"]["file_path"] = "/tmp/elsewhere-memory.md"
        self.assertEqual(decide(data, "all", True, [])[:2], (3, "outside"))

    def test_all_mode_after_cd_gates_a_sibling_under_the_project_dir(self):
        self.write(self.parent, [user("fix it")])
        os.makedirs(os.path.join(self.root, "src"))
        data = self.payload("lib/x.go")
        data["cwd"] = os.path.join(self.root, "src")
        self.assertEqual(decide(data, "all", True, [])[:2], (3, "outside"))
        os.environ["CLAUDE_PROJECT_DIR"] = self.root
        code, word, message = decide(data, "all", True, [])
        self.assertEqual((code, word), (2, "edit-gate-no-spec-call"))
        self.assertIn("files outside " + self.root + ",", message)
        self.assertIn("If validate_task_spec is not available in this session, stop and tell the\nuser", message)

    def test_fingerprint_without_edit_gate_keeps_every_path_gated(self):
        self.sub([user(CLAUSE)])
        self.assertEqual(decide(self.payload("docs/x.md", agent="a1"), "off", True, ["docs/**/*.md"])[0], 2)

    def test_session_guard_off_with_all_still_gates_through_the_edit_gate(self):
        self.sub([user(CLAUSE)])
        self.assertEqual(decide(self.payload(agent="a1"), "all", False, [])[:2], (2, "edit-gate-no-spec-call"))

    def test_session_guard_off_with_gate_off_does_not_gate(self):
        self.sub([user(CLAUSE)])
        self.assertEqual(decide(self.payload(agent="a1"), "off", False, [])[0], 3)


def tool_result_text(tool_use_id, text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": tool_use_id, "content": [{"type": "text", "text": text}]}]}})


if __name__ == "__main__":
    unittest.main()
