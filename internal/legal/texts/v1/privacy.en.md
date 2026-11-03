# Aquarius Privacy Policy

Version 1 · Effective 3 November 2026

This policy explains how Aquarius ("the Program") handles your information. **The conclusion first: the Program does not collect your personal information. Your conversations, attachments, memories and credentials stay on your machine. Content leaves the machine only when you initiate it, and only to a provider you configured yourself.**

## 1. Who processes

The Program is published by the author of Aquarius (a natural person; GitHub: @Tonyjh07). Contact: yjh07@qq.com. Project home: https://github.com/Tonyjh07/Aquarius . The controller referred to in this policy is that natural person; contact details are given in Section 13.

The Program is stand-alone software. It has no accounts and no server side. We operate no server that receives your conversation content.

## 2. Summary

2.1 We do not collect, upload or resell your conversation content, attachments, memories, credentials or usage records.
2.2 The Program contains no usage analytics, crash reporting, update checking or advertising components.
2.3 Your data exists only on your machine by default, and control over it rests with you.

## 3. What is stored locally

3.1 All of the following is stored on your machine, under the data root `~/.aquarius/`. In the table, `<name>` is the name of a configuration profile; the default profile is called `default`.

| Content | Default location (under the data root `~/.aquarius/`) | Notes |
|---|---|---|
| Conversations | `profiles/<name>/conversations/<id>.json` | Messages, branches, tool calls and results; the last 3 generations are kept as backups (`.bak` / `.bak.1` / `.bak.2`) |
| Attachments | `profiles/<name>/attachments/` | Original bytes of images and documents, addressed by content |
| Memories | `profiles/<name>/memories.md`, `profiles/<name>/conversations/<id>.memory.md` | Plain markdown; you can edit them directly |
| Configuration | `profiles/<name>/config.json` | Service addresses, model names, credential references |
| Credentials | `profiles/<name>/secrets.bin` | Encrypted on this machine; not readable after moving to another machine |
| Audit log | `profiles/<name>/audit.log` | Records of tool calls |
| Job logs | `profiles/<name>/jobs/` | Output of background jobs |
| Run logs | `profiles/<name>/logs/aquarius-YYYY-MM-DD.log` | Warnings from startup, assembly and the model chain; split by day, kept for 7 days |
| Window state | `profiles/<name>/gui_pos.json` | Where the window last sat |

3.2 The data directory can be changed with the `-data` launch argument. `-profile` isolates separate configurations from one another. The `config.json` at the data root only records the current profile name; profiles do not affect each other.

## 4. What leaves the machine

4.1 Only the five categories below, each triggered by an action of yours: sending a message, recording, reading aloud, using a tool, installing an extension.

| Content | Recipient | Trigger |
|---|---|---|
| Messages, attachment text or images, screenshots, text to be read aloud, recorded audio | The model or speech provider you configured, plus any fallback provider you enabled | Sending a message, reading aloud, recording |
| Recorded audio | The cloud recognition provider you selected | Recording (with local whisper selected, audio does not leave the machine) |
| Search terms and target page addresses | The search or fetch provider you configured | Web search, page fetching |
| Requests to download a skill package | The URL you supplied (for example GitHub) | Installing or updating a skill |
| Tool requests and results | The MCP server you configured (a local process, or a network endpoint you supplied) | The model calls that tool |

4.2 We do not relay, store or retain copies. These transfers go directly from your machine to the provider concerned.
4.3 The default configuration pre-fills the endpoint of one provider, DeepSeek, purely for convenience. You can change the address and the model at any time, including to a service running entirely on your machine.
4.4 If a provider you configure is located outside your country or region, the content above will be transferred there. That is your choice; you must judge for yourself whether it meets the requirements applicable to you, and we neither take part in that transfer nor can we complete any related formalities on your behalf. We cannot name the overseas recipient in advance, because which provider receives the content is entirely your choice; please refer to that provider's privacy policy.

## 5. What we do not do

5.1 We do not build user profiles, run targeted advertising or make automated decisions.
5.2 We do not provide, sell or disclose your information to third parties.
5.3 We do not read your conversations to train or improve models.
5.4 We do not sync across devices and do not back up to the cloud.

## 6. Third-party services and you

6.1 You are the party in a direct relationship with the providers listed above. Their privacy policies and terms govern their handling of your information; we are not their agent, reseller or joint controller.
6.2 We cannot control how those providers handle your content, and we cannot exercise your rights against them on your behalf. Address such requests to the provider concerned.

## 7. Sensitive personal information

7.1 Screenshots, recordings and file contents may contain sensitive personal information, such as biometric data, health data, financial account data or location traces. The Program's defaults are: screenshots and high-risk tools are confirmed case by case, and recording can use local recognition, in which case the audio does not leave the machine.
7.2 Do not submit another person's sensitive personal information to a model unless you are entitled to do so. If you do, you must obtain consent yourself and bear the consequences.
7.3 Credentials are kept in the local encrypted keystore; they are not written to logs and do not enter conversation files.

## 8. Minors

The Program is not offered to children under 14, and we do not knowingly collect their personal information. If you are the guardian of a minor and find that they are using the Program, please guide them to stop, or to use it under your supervision.

## 9. Your rights and how to exercise them

9.1 You may exercise your rights as follows:

| Right | How to exercise it |
|---|---|
| Access, copy | Conversations are JSON, memories are markdown, attachments are original bytes: open the data directory, or use the export function to package them |
| Rectify, supplement | Edit the files directly, or revise a message to create a new branch |
| Erase | Delete the relevant files, or delete the whole data directory |
| Withdraw consent | Turn off the corresponding feature (speech, screen capture, web search, external agents), or uninstall and delete the data directory |
| Explanation | Write to us |

9.2 **Because the data is not on our servers, we cannot access, rectify or erase it for you.** This is not evasion but the necessary consequence of a local-first design: those operations can only be performed by you, on your machine. If you run into difficulty, contact us and we will do our best to explain how.

## 10. Security measures

10.1 The keystore is encrypted locally; credentials are never written to logs or to conversation files; tool calls are recorded in an audit log; four permission levels with case-by-case confirmation; attachments are stored by content address; unreferenced attachments are cleaned up at startup.
10.2 Note that the security of your machine ultimately depends on your device and account. A shared computer, a weak password or malware can defeat the measures above.

## 11. Retention

Retention is up to you. The Program applies no automatic expiry and does not clean up conversations or memories by itself. Once you uninstall and delete the data directory, the data is gone.

## 12. Changes

12.1 This policy may be revised as the Program changes. A revised version is released with a new build and presented for reading on first run.
12.2 If the purpose, the manner or the categories of processing change materially, we will give separate notice and ask for your consent again.
12.3 Each version carries a version number and an effective date. When the version changes, the first run asks you to read and confirm it again.

## 13. Complaints and contact

- Email: yjh07@qq.com
- Issues: https://github.com/Tonyjh07/Aquarius/issues

We respond within 15 business days of receipt. If you believe our handling infringes your rights, you may also complain to a supervisory authority or bring proceedings under the law.

## 14. Effective date and version

Version 1, effective 3 November 2026. This policy is written in Chinese. This English text is a translation for convenience; the Chinese version prevails.
