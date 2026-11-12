# Trello workflow for bekup development

## Context
The user wants to steer bekup development through the Trello board **Bekop**
(https://trello.com/b/eSsbJdKr/bekop, workspace "Trello Workspace", user tz
Europe/Amsterdam). Lists (ids are ARIs under workspace `6af06965785808eaed8209cf`):

| List | objectId | Use |
|---|---|---|
| Idea's | 6af0698c9349fe91ebc8b83a | user drops ideas; discussed via card comments |
| In planning | 6af0698c9349fe91ebc8b83b | idea agreed, being planned |
| Todo | 6af06a8ed30dc95bd0e69313 | concrete task cards made from the plan |
| In progress | 6af069a4149ccad1a5d6dfec | task being worked on (one at a time, own branch, D-230) |
| Open questions | 6af069b3d8cc21eab7985a80 | a card with a question for the user |
| Klaar | 6af0698c9349fe91ebc8b83c | done (AC met, reviews passed) |

All lists are currently empty. No repo code changes are part of this plan.

## Decisions (from the user)
- Poll Idea's every 5 min, **every day 08:00–22:00** local.
- On something new: **comment on the card right away** (analysis + questions), and report here.
- Also pick up **new user comments** on existing idea cards.
- **Durable** cron (survives restarts; auto-expires after 7 days → I remind the user to renew).

## Implementation
1. **Recognising "new" without a state file.** The Trello MCP acts as the user's own
   account, so my comments can't be told apart by author. Every comment I post starts
   with the marker `🤖 Claude:`. An idea card needs attention when it has no comments,
   or its newest comment does not start with the marker. That keeps the check stateless
   and the conversation history on Trello.
2. **Cron job** (`CronCreate`, cron `*/5 8-21 * * *`, recurring, durable). The prompt tells me to:
   - spawn one light agent (`general-purpose`, model `haiku`) that only reads
     (`trelloReadCard list_by_list` on Idea's, plus `list_comments` per card) and returns
     the cards needing attention (id, url, title, description, the new user comments).
     It writes nothing.
   - If it finds nothing, end quietly. Otherwise, for each card: read the relevant
     code/docs/DECISIONS as needed and post a `🤖 Claude:` comment (in Dutch) with
     my understanding, how it fits the architecture/security rules, and open questions.
     Then give a short report in the session.
3. **Lane rules for later work** (I follow these by hand, not via cron):
   - When the user says an idea is ready → move it to **In planning**, write the plan
     (plan file + DECISIONS `proposed`), as a comment/checklist on the card.
   - After approval → make **Todo** cards per task (title = plan task id + slug, link
     back to the idea card); idea card stays in In planning as the epic.
   - Start a task → **In progress** (branch `feature/p3-<id>-<slug>`).
   - Blocked on the user → copy/move a question card to **Open questions** (comment on the card it came from).
   - AC met + dual review done → **Klaar**. Committing stays with the user (memory).
4. **Memory**: save a `project` memory `trello-workflow` (board URL, list ids, lane
   rules, comment marker, cron and its 7-day expiry) and add it to MEMORY.md.

## Verification
- `CronList` shows the job with its next run time.
- Run the check prompt once by hand right away: the agent should report "nothing new"
  (Idea's is empty).
- Ask the user to add a test idea; on the next run a `🤖 Claude:` comment shows up
  on that card. A user reply should be picked up on the run after that.
