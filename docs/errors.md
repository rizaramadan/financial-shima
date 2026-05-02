# Error Code Registry

Every error emission point in `financial-shima` carries a stable
`FS-NNNN` code. The code lands in two places at once:

1. **JSON body** — `/api/v1` responses include a `site` field next to
   the `error` category and the human `message`:
   ```json
   {"site": "FS-0042", "error": "validation_failed", "message": "type must be ..."}
   ```
2. **Server log line** — every `c.Logger().Errorf` (or `mw.LogError`)
   prefixes the same `[FS-NNNN]` so log greps and HTTP responses
   share one pinpoint.

When something breaks: copy the code → grep this table → file:line of
the exact emission. No need to parse the message.

## Allocation buckets

| Range          | Area                              |
|----------------|-----------------------------------|
| `FS-0001..09`  | API key / auth middleware         |
| `FS-0010..19`  | `/api/v1/accounts`                |
| `FS-0020..29`  | `/api/v1/pos`                     |
| `FS-0030..39`  | `/api/v1/counterparties`          |
| `FS-0040..89`  | `/api/v1/transactions`            |
| `FS-0090..99`  | `/api/v1/balances`                |
| `FS-0100..99`  | `/api/v1/income-templates`        |
| `FS-0200..99`  | Web (HTML) handlers               |
| `FS-0300..`    | Reserved (Phase B: reverse / edit / inter-Pos) |

Two emission points must never share a code — pinpoint guarantee
depends on uniqueness.

## Registry

| Code     | File                                              | HTTP | Category               | Meaning                                                  |
|----------|---------------------------------------------------|-----:|------------------------|----------------------------------------------------------|
| FS-0001  | `web/middleware/apikey.go`                        |  401 | multiple_api_key_headers | More than one `x-api-key` header sent                  |
| FS-0002  | `web/middleware/apikey.go`                        |  401 | missing_api_key        | `x-api-key` header absent or empty                       |
| FS-0003  | `web/middleware/apikey.go`                        |  401 | invalid_api_key        | `x-api-key` does not match expected secret               |
| FS-0010  | `web/handler/api_accounts.go`                     |  503 | service_unavailable    | GET /accounts: DB unwired                                |
| FS-0011  | `web/handler/api_accounts.go`                     |  500 | internal_error         | GET /accounts: ListAccounts query failed                 |
| FS-0012  | `web/handler/api_accounts.go`                     |    — | warn                   | GET /accounts: row with invalid uuid skipped             |
| FS-0013  | `web/handler/api_accounts_post.go`                |  503 | service_unavailable    | POST /accounts: DB unwired                               |
| FS-0014  | `web/handler/api_accounts_post.go`                |  400 | validation_failed      | POST /accounts: malformed JSON body                      |
| FS-0015  | `web/handler/api_accounts_post.go`                |  400 | validation_failed      | POST /accounts: name field empty                         |
| FS-0016  | `web/handler/api_accounts_post.go`                |  500 | internal_error         | POST /accounts: CreateAccount query failed               |
| FS-0020  | `web/handler/api_pos_list.go`                     |  503 | service_unavailable    | GET /pos: DB unwired                                     |
| FS-0021  | `web/handler/api_pos_list.go`                     |  500 | internal_error         | GET /pos: ListPos query failed                           |
| FS-0022  | `web/handler/api_pos_list.go`                     |    — | warn                   | GET /pos: row with invalid uuid skipped                  |
| FS-0023  | `web/handler/api_pos_post.go`                     |  503 | service_unavailable    | POST /pos: DB unwired                                    |
| FS-0024  | `web/handler/api_pos_post.go`                     |  400 | validation_failed      | POST /pos: malformed JSON body                           |
| FS-0025  | `web/handler/api_pos_post.go`                     |  400 | validation_failed      | POST /pos: logic.Validate violation                      |
| FS-0026  | `web/handler/api_pos_post.go`                     |  409 | conflict               | POST /pos: (name, currency) UNIQUE collision             |
| FS-0027  | `web/handler/api_pos_post.go`                     |  500 | internal_error         | POST /pos: CreatePos query failed (other than 23505)     |
| FS-0030  | `web/handler/api_counterparties_list.go`          |  503 | service_unavailable    | GET /counterparties: DB unwired                          |
| FS-0031  | `web/handler/api_counterparties_list.go`          |  500 | internal_error         | GET /counterparties: ListCounterparties / Search failed  |
| FS-0032  | `web/handler/api_counterparties_post.go`          |  503 | service_unavailable    | POST /counterparties: DB unwired                         |
| FS-0033  | `web/handler/api_counterparties_post.go`          |  400 | validation_failed      | POST /counterparties: malformed JSON body                |
| FS-0034  | `web/handler/api_counterparties_post.go`          |  400 | validation_failed      | POST /counterparties: name empty                         |
| FS-0035  | `web/handler/api_counterparties_post.go`          |  400 | validation_failed      | POST /counterparties: name fails regex (§4.4)            |
| FS-0036  | `web/handler/api_counterparties_post.go`          |  500 | internal_error         | POST /counterparties: GetOrCreate query failed           |
| FS-0040  | `web/handler/api_transactions_list.go` (pre-DB)   |  400 | validation_failed      | GET /transactions: from must be YYYY-MM-DD               |
| FS-0041  | `web/handler/api_transactions_list.go` (pre-DB)   |  400 | validation_failed      | GET /transactions: to must be YYYY-MM-DD                 |
| FS-0042  | `web/handler/api_transactions_list.go` (pre-DB)   |  400 | validation_failed      | GET /transactions: type unknown                          |
| FS-0043  | `web/handler/api_transactions_list.go`            |  503 | service_unavailable    | GET /transactions: DB unwired                            |
| FS-0044  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: from must be YYYY-MM-DD               |
| FS-0045  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: to must be YYYY-MM-DD                 |
| FS-0046  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: type unknown                          |
| FS-0047  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: account_id not UUID                   |
| FS-0048  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: pos_id not UUID                       |
| FS-0049  | `web/handler/api_transactions_list.go`            |  400 | validation_failed      | GET /transactions: counterparty_id not UUID              |
| FS-0050  | `web/handler/api_transactions_list.go`            |  500 | internal_error         | GET /transactions: ListTransactionsByDateRange failed    |
| FS-0060  | `web/handler/api_transactions_post.go`            |  503 | service_unavailable    | POST /transactions: DB unwired                           |
| FS-0061  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: malformed JSON body                  |
| FS-0062  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: type must be money_in/out            |
| FS-0063  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: idempotency_key empty                |
| FS-0064  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: effective_date format               |
| FS-0065  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: account_id not UUID                  |
| FS-0066  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: pos_id not UUID                      |
| FS-0067  | `web/handler/api_transactions_post.go`            |  404 | not_found              | POST /transactions: account does not exist               |
| FS-0068  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: GetAccount failed                    |
| FS-0069  | `web/handler/api_transactions_post.go`            |  404 | not_found              | POST /transactions: pos does not exist                   |
| FS-0070  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: GetPos failed                        |
| FS-0071  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: counterparty_id not UUID             |
| FS-0072  | `web/handler/api_transactions_post.go`            |  404 | not_found              | POST /transactions: counterparty (by id) not found       |
| FS-0073  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: counterparty lookup failed           |
| FS-0074  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: GetOrCreateCounterparty failed       |
| FS-0075  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: counterparty id+name both empty      |
| FS-0076  | `web/handler/api_transactions_post.go`            |  400 | validation_failed      | POST /transactions: §5.1 logic violation                 |
| FS-0077  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: ledger.Insert failed                 |
| FS-0078  | `web/handler/api_transactions_post.go`            |  500 | internal_error         | POST /transactions: post-insert re-fetch failed          |
| FS-0090  | `web/handler/api_balances.go`                     |  503 | service_unavailable    | GET /balances: DB unwired                                |
| FS-0091  | `web/handler/api_balances.go`                     |  500 | internal_error         | GET /balances: SumAccountBalances failed                 |
| FS-0092  | `web/handler/api_balances.go`                     |  500 | internal_error         | GET /balances: SumPosCashBalances failed                 |
| FS-0093  | `web/handler/api_balances.go`                     |  500 | internal_error         | GET /balances: SumAccountBalancesByPosCurrency failed    |
| FS-0094  | `web/handler/api_balances.go`                     |  500 | internal_error         | GET /balances: ListAccounts failed                       |
| FS-0095  | `web/handler/api_balances.go`                     |  500 | internal_error         | GET /balances: ListPos failed                            |
| FS-0100  | `web/handler/api_income_templates.go`             |  503 | service_unavailable    | GET /income-templates: DB unwired                        |
| FS-0101  | `web/handler/api_income_templates.go`             |  500 | internal_error         | GET /income-templates: ListIncomeTemplates failed        |
| FS-0102  | `web/handler/api_income_templates.go`             |    — | warn                   | GET /income-templates: ListIncomeTemplateLines per-row warn |
| FS-0110  | `web/handler/api_income_templates.go`             |  503 | service_unavailable    | POST /income-templates: DB unwired                       |
| FS-0111  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: malformed JSON body              |
| FS-0112  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: name empty                       |
| FS-0113  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: zero lines                       |
| FS-0114  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: line amount non-positive         |
| FS-0115  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: line pos_id not UUID             |
| FS-0116  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | POST /income-templates: leftover_pos_id not UUID         |
| FS-0117  | `web/handler/api_income_templates.go`             |  500 | internal_error         | POST /income-templates: BeginTx failed                   |
| FS-0118  | `web/handler/api_income_templates.go`             |  409 | conflict               | POST /income-templates: name UNIQUE collision            |
| FS-0119  | `web/handler/api_income_templates.go`             |  500 | internal_error         | POST /income-templates: CreateIncomeTemplate failed      |
| FS-0120  | `web/handler/api_income_templates.go`             |  409 | conflict               | POST /income-templates: duplicate Pos in lines           |
| FS-0121  | `web/handler/api_income_templates.go`             |  500 | internal_error         | POST /income-templates: AddIncomeTemplateLine failed     |
| FS-0122  | `web/handler/api_income_templates.go`             |  500 | internal_error         | POST /income-templates: tx.Commit failed                 |
| FS-0130  | `web/handler/api_income_templates.go`             |  503 | service_unavailable    | POST /income-templates/:id/apply: DB unwired             |
| FS-0131  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: id path param not UUID                            |
| FS-0132  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: malformed JSON body                               |
| FS-0133  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: idempotency_key empty                             |
| FS-0134  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: effective_date format                            |
| FS-0135  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: account_id not UUID                               |
| FS-0136  | `web/handler/api_income_templates.go`             |  404 | not_found              | apply: template not found                                |
| FS-0137  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: GetIncomeTemplate failed                          |
| FS-0138  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: ListIncomeTemplateLines failed                    |
| FS-0139  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: amount < sum(lines)                               |
| FS-0140  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: amount > sum(lines), no leftover Pos              |
| FS-0141  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: template has no lines                             |
| FS-0142  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: amount non-positive                               |
| FS-0143  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: logictpl.Apply unknown error                      |
| FS-0144  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: GetPos for allocation failed                      |
| FS-0145  | `web/handler/api_income_templates.go`             |  404 | not_found              | apply: account not found                                 |
| FS-0146  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: GetAccount failed                                 |
| FS-0147  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: counterparty_id not UUID                          |
| FS-0148  | `web/handler/api_income_templates.go`             |  404 | not_found              | apply: counterparty (by id) not found                    |
| FS-0149  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: counterparty lookup failed                        |
| FS-0150  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: GetOrCreateCounterparty failed                    |
| FS-0151  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: counterparty id+name both empty                   |
| FS-0152  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: non-IDR Pos line rejected (FX out of scope)       |
| FS-0153  | `web/handler/api_income_templates.go`             |  400 | validation_failed      | apply: §5.1 logic violation per allocation               |
| FS-0154  | `web/handler/api_income_templates.go`             |  500 | internal_error         | apply: ledger.Insert failed mid-loop                     |
| FS-0200  | `web/handler/home.go`                             |    — | log                    | home loadHomeData failed                                 |
| FS-0201  | `web/handler/notifications.go`                    |    — | log                    | ListNotificationsForUser failed                          |
| FS-0202  | `web/handler/notifications.go`                    |    — | log                    | MarkNotificationRead failed                              |
| FS-0203  | `web/handler/notifications.go`                    |    — | log                    | UnreadCount failed                                       |
| FS-0204  | `web/handler/notifications.go`                    |    — | log                    | MarkAllNotificationsRead failed                          |
| FS-0210  | `web/handler/pos.go`                              |    — | log                    | GetPos failed                                            |
| FS-0211  | `web/handler/pos.go`                              |    — | log                    | GetPosCashBalance failed                                 |
| FS-0212  | `web/handler/pos.go`                              |    — | log                    | list obligations query failed                            |
| FS-0213  | `web/handler/pos.go`                              |    — | log                    | scan obligation row failed                               |
| FS-0214  | `web/handler/pos.go`                              |    — | log                    | ListTransactionsByPos failed                             |
| FS-0220  | `web/handler/pos_new.go`                          |    — | log                    | CreatePos (web form) failed                              |
| FS-0230  | `web/handler/spending.go`                         |    — | log                    | SumMoneyOutByPosMonth failed                             |
| FS-0240  | `web/handler/transactions.go`                     |    — | log                    | ListTransactionsByDateRange failed                       |
| FS-0250  | `web/handler/income_template_web.go`              |    — | log                    | list income templates failed                             |
| FS-0251  | `web/handler/income_template_web.go`              |    — | log                    | list pos failed                                          |
| FS-0252  | `web/handler/income_template_web.go`              |    — | log                    | create template failed                                   |
| FS-0253  | `web/handler/income_template_web.go`              |    — | log                    | add line failed                                          |
| FS-0254  | `web/handler/income_template_web.go`              |    — | log                    | get template failed                                      |
| FS-0255  | `web/handler/income_template_web.go`              |    — | log                    | apply row failed                                         |
| FS-0260  | `web/handler/income_template_web.go`              |  500 | (HTML)                 | database not configured (web form)                       |
| FS-0261  | `web/handler/income_template_web.go`              |  500 | (HTML)                 | could not load template (web view)                       |

## Adding a new code

1. Pick the next unused number in the right bucket. If a bucket is
   exhausted, add a new range above and document it here.
2. Embed the code in **both** the `WriteAPIError(... "FS-NNNN", ...)`
   call and any paired `mw.LogError(... "FS-NNNN", ...)`. For HTML
   handlers, prefix the log line and (where rendered) the error
   message with `[FS-NNNN]`.
3. Add the row to the registry above. Keep the table sorted by code.
4. Two emission points must never share a code.
