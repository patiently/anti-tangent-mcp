import json
import unittest

from check_task_start import classify, subagent_transcript, FULL_HEADING, LITE_HEADING, SPEC_TOOL


def user(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": text}})


def user_parts(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": text}]}})


def tool_use(name, inp=None):
    return json.dumps({"type": "assistant", "message": {"role": "assistant", "content": [
        {"type": "tool_use", "id": "t1", "name": name, "input": inp or {}}]}})


CLAUSE = "Implement Task 3.\n\n" + FULL_HEADING + "\n\nAt task start ..."


class ClassifyTest(unittest.TestCase):
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


class SubagentTranscriptTest(unittest.TestCase):
    def test_subagent_transcript_sits_under_the_parent_stem(self):
        got = subagent_transcript({"transcript_path": "/p/s1.jsonl", "session_id": "s1", "agent_id": "a1"})
        self.assertEqual(got, "/p/s1/subagents/agent-a1.jsonl")

    def test_main_session_has_no_subagent_transcript(self):
        self.assertEqual(subagent_transcript({"transcript_path": "/p/s1.jsonl", "session_id": "s1"}), "")

    def test_agent_id_that_is_not_an_identifier_is_refused(self):
        self.assertEqual(subagent_transcript({"transcript_path": "/p/s1.jsonl", "agent_id": "../x"}), "")


if __name__ == "__main__":
    unittest.main()
