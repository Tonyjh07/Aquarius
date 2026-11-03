# Aquarius Terms of Service

Version 1 · Effective 3 November 2026

These Terms are entered into between the author of Aquarius (a natural person; GitHub: @Tonyjh07, "we", "us") and you ("you"), and govern your installation, execution and other use of the Aquarius program ("the Program"). **Please read these Terms and the Aquarius Privacy Policy in full before first use. By ticking the consent box or by using the Program, you confirm that you have read, understood and accepted these Terms in full. If you do not agree, do not use the Program.**

## 1. Scope

1.1 These Terms set out the rules of use of the Program and the allocation of risk between you and us.
1.2 The Program's source code and binaries are licensed separately under the Mozilla Public License 2.0 ("MPL-2.0"), as described in Section 2. These Terms neither replace nor limit that licence.
1.3 The Aquarius Privacy Policy forms part of these Terms and applies together with them.

## 2. Licence and open source

2.1 The Program is released under MPL-2.0. The rights that licence grants you — to use, copy, modify and distribute — **are not restricted by these Terms**; these Terms govern only your conduct when using the Program and the allocation of risk.
2.2 When you distribute a binary of the Program, give recipients the means to obtain the source code as required by MPL-2.0 Section 3.2(b). When you modify files covered by the MPL, make them available under the same licence as required by Section 3.1.
2.3 The name "Aquarius" and the application icon fall outside the scope of MPL-2.0 and remain with the author. You may build derivative versions, but please use a different name and icon so that the origin is not confused.

## 3. Operating requirements and your responsibilities

3.1 The Program contains no model of its own. You must arrange your own model service, speech service or other external service, and obtain the credentials to access them.
3.2 Fees, quotas, availability and data handling as between you and those providers are agreed and settled between you and them alone. We do not act as an intermediary, reseller or payment agent.
3.3 Keep your credentials secure. They are stored on your machine, in the local encrypted keystore or in an environment variable of the same name. Loss caused by disclosure of your credentials is yours.
3.4 You are responsible for confirming that the services you use and the content you process comply with the rules that apply to you.

## 4. Acceptable use

4.1 Do not use the Program for unlawful activity, or to generate or disseminate information that the law prohibits.
4.2 Do not use the Program to infringe the intellectual property, privacy, reputation, likeness or other personal or proprietary rights of others.
4.3 Do not bypass the Program's permission prompts, and do not modify the Program to evade its safety design for use on another person's device.
4.4 When you submit another person's personal information to a model, you must have a lawful basis for doing so. Responsibility for that submission rests with you.
4.5 When you publish content generated with the Program, mark it as required by the platform and the jurisdiction in which you publish.

## 5. Permissions, tools and execution risk

5.1 The Program can read and write files, execute commands, manage background jobs, capture the screen and access the network. These capabilities are governed by a permission level: `read-only`, `strict` (the default), `permissive` or `full-access`. Under `strict`, calls outside the permitted scope are confirmed one by one; in unattended mode they are refused outright, with the reason stated.
5.2 **Command execution is not sandboxed.** Commands run under your current operating-system user identity. The path scope a tool declares is only the criterion by which we refuse out-of-scope calls; it is not operating-system isolation. **Granting `full-access`, or approving a confirmation, authorises those operations to take place on your machine for real.**
5.3 External agents connected over ACP run their own tool loops, and their behaviour is not fully reviewed by the Program. Approving a delegation means accepting that the agent acts on its own judgement.
5.4 These operations may modify or delete files, incur charges or send data over the network. **Back up important data before use, and read what you are approving before you approve it.**

## 6. Extensions and third-party components

6.1 Skills, MCP servers and external agents are provided by third parties. We make no warranty as to their content, behaviour, availability or security, and accept no liability for their consequences.
6.2 Third-party components may carry their own licences and terms, which you must also comply with.
6.3 When you install a skill from a URL or connect an MCP server, judge the source for yourself; the Program does not review what they execute.

## 7. Data and backup

7.1 Your conversations, attachments, memories, configuration and credentials are stored in a local directory (by default `~/.aquarius/`, changeable with `-data`). We hold no copy of that data.
7.2 **Back it up yourself.** A defect in the Program, a disk failure, a mistake, concurrent writes or uninstallation may make data unrecoverable, and we cannot restore data you have not backed up. Copy the data directory from time to time, or use the export function to produce a readable archive.
7.3 Uninstalling the Program does not delete the data directory. To remove everything, delete that directory yourself.

## 8. Generated content

8.1 Generated content is produced by the model service you configure. It may be wrong, outdated, one-sided or contrary to fact. **It is not medical, legal, financial or tax advice; consult a qualified professional on such matters.**
8.2 We do not warrant the accuracy, completeness, lawfulness or fitness for purpose of generated content, and we are not responsible for decisions you take on the basis of it.
8.3 Ownership of generated content is determined by your agreement with the model provider and by applicable law.

## 9. Disclaimer and limitation of liability

9.1 **The Program is provided "as is". To the maximum extent permitted by law, we make no express or implied warranty, including as to merchantability, fitness for a particular purpose, non-infringement and error-free operation.**
9.2 **To the extent permitted by law, we are not liable for indirect, incidental or consequential loss, loss of profit, loss of goodwill or loss of data arising from the use of, or inability to use, the Program; our aggregate liability in connection with the Program shall not exceed the amount you actually paid to use it (the Program is provided free of charge, so that amount is zero).**
9.3 **This section does not apply to personal injury or property loss caused by our wilful misconduct or gross negligence, does not exclude liability that cannot be excluded by agreement, and does not affect any right you have as a consumer that cannot be waived.**

## 10. Changes to these Terms

10.1 We may revise these Terms. A revised version is released with a new build of the Program and presented for reading on first run.
10.2 For material changes affecting your principal rights or obligations, we will give separate notice and ask for your consent again, rather than treating continued use as acceptance.
10.3 If you do not accept a revision, stop using the Program and delete it.

## 11. Termination

11.1 These Terms end when you stop using the Program.
11.2 We cannot terminate your use of a copy remotely, nor delete your data remotely: the Program has no accounts, and we do not hold your data.
11.3 Sections 2 (Licence), 8 (Generated content), 9 (Disclaimer and limitation of liability) and 12 (Governing law and disputes) survive termination.

## 12. Governing law and disputes

12.1 These Terms are governed by the laws of the People's Republic of China.
12.2 Disputes arising from these Terms should first be raised with us. Failing resolution, **either party may bring proceedings in the competent people's court at the defendant's domicile**.
12.3 If mandatory law in your jurisdiction gives you more favourable rights or readier remedies, that law prevails.

## 13. Contact and complaints

- Email: yjh07@qq.com
- Project home and issue tracker: https://github.com/Tonyjh07/Aquarius

We respond within 15 business days of receipt.

## 14. Miscellaneous

14.1 If any provision is held invalid, the remaining provisions remain in force.
14.2 These Terms and the Privacy Policy are the entire agreement between you and us concerning the Program.
14.3 These Terms are written in Chinese. This English text is a translation for convenience; the Chinese version prevails.
14.4 These Terms take effect on the date stated above.
