# Calendium Feature Map

Calendium is a keyboard-first email + calendar manager targeting **full feature parity with Superhuman** for email, fused with a **best-of-breed calendar** that borrows the strongest ideas from Fantastical, Notion Calendar, Vimcal, Amie, Rise, Apple Calendar, and Google Calendar. The bet: Superhuman proved that speed (sub-100ms interactions), a command palette, and opinionated triage make email feel effortless — but its calendar is an accessory. Calendium makes the calendar a first-class peer of the inbox, sharing one design language (shadcn new-york, neutral, light+dark), one Go backend, and one `@calendium/shared` contract across web, desktop, and mobile — all unlocked by a single $50/yr Stripe subscription (Spotify model, no IAP). This document is the parity checklist and the roadmap for getting there.

**Status legend** — `shipped`: implemented end-to-end in this codebase (real data path, tested). `scaffolded`: the model, API contract, and/or UI for the feature exists in this codebase today (M0, mock/local data where sync is not yet live). `planned`: not yet in the codebase; targeted at the milestone noted in the roadmap.

## Superhuman email parity

| Feature | What it is | Priority | Status | Platforms |
| --- | --- | --- | --- | --- |
| **Triage** | | | | |
| Split Inbox | Auto-triages incoming mail into customizable split tabs (Team, VIPs, tools) so users batch-process similar mail; modeled as `InboxSplit` (`important/vip/team/calendar/news/social/other`) with classification at sync ingest | P0 | scaffolded | web, desktop, mobile |
| Important vs Other classification | AI classifies mail into Important and Other streams so newsletters and noise never bury human, high-priority conversations; covered by the split model's `important`/`other` splits + heuristic/AI ingest pass | P0 | scaffolded | web, desktop, mobile |
| Snooze | Snooze any conversation with two keystrokes using natural-language times ("tomorrow morning", "2 days"); `POST /v1/mail/threads/{id}/snooze`, worker wake-ups return it to the inbox | P0 | scaffolded | web, desktop, mobile |
| Remind Me follow-up reminders | Pick a time when sending and the thread resurfaces if no reply arrives; `remindAt` on threads + reminder endpoint | P0 | scaffolded | web, desktop, mobile |
| Get Me To Zero | One command bulk-archives all email older than a chosen period for instant inbox zero on day one | P1 | scaffolded | web, desktop, mobile |
| Bulk triage actions | Select ranges of emails and archive / mark read / label in bulk with keyboard shortcuts | P1 | scaffolded | web, desktop |
| One-click and bulk unsubscribe | Unsubscribe from senders instantly and bulk-archive their past mail in the same action | P1 | scaffolded | web, desktop, mobile |
| Inbox zero celebration design | An empty inbox reveals rotating imagery, making inbox zero a rewarding destination | P1 | scaffolded | web, desktop, mobile |
| Contact pane with social insights | Sidebar with sender photo, role, company, location, bio, social links, and recent conversations; `ContactPane` (avatar via gravatar→favicon→initials, company from domain, aggregate stats, 5 most recent conversations) sourced from `GET /v1/mail/contacts/{email}` | P1 | scaffolded | web, desktop |
| Auto-advance | After acting on an email the next conversation opens automatically, keeping triage in flow | P2 | scaffolded | web, desktop, mobile |
| Reorderable splits | Reorder all Split Inboxes (incl. Inbox, Important, Other, Reminders) to match your workflow | P2 | scaffolded | web, desktop, mobile |
| Stars and labels via shortcuts | Star, label, and move conversations entirely from the keyboard (actions API exists; shortcut surface planned) | P2 | scaffolded | web, desktop |
| **Compose** | | | | |
| Snippets | Reusable templates with variables like `{first_name}`, insertable by shortcut, prefilling subject/recipients/attachments; `Snippet` type + CRUD endpoints | P0 | scaffolded | web, desktop, mobile |
| Send Later | Schedule any message to send at a chosen future time with natural-language input; `scheduledAt` on drafts + worker scheduled-send loop | P0 | scaffolded | web, desktop, mobile |
| Undo Send | A 10-second window (hit Z) to recall a just-sent email before it actually leaves | P0 | scaffolded | web, desktop, mobile |
| Read statuses | See when recipients open your emails, how many times, on which device; `openedAt` on messages | P0 | scaffolded | web, desktop, mobile |
| Smart Send | Recommends the best send time from the recipient's activity patterns and time zone; `GET /v1/mail/send-suggestion` feeds a debounced compose nudge bucketed into the recipient's local morning/afternoon/evening | P1 | scaffolded | web, desktop, mobile |
| Instant Intro | One command replies to an introduction, thanks the introducer, moves them to BCC; `buildInstantIntro` (`@calendium/shared`) drafts the reply from thread context | P1 | scaffolded | web, desktop |
| Recent Opens feed | Live feed of who recently opened your emails, for well-timed follow-ups; `OpensFeed` right-side panel (`G O`) pages `GET /v1/mail/opens` via a keyset cursor | P1 | scaffolded | web, desktop, mobile |
| Auto Bcc | Automatically BCC a configured address (e.g. CRM logging) on every outgoing email; per-account `autoBcc` applied server-side at delivery (canonicalized) | P2 | scaffolded | web, desktop, mobile |
| Emoji reactions | React to emails with emoji for lightweight acknowledgment without a full reply; `message_reactions` with truthful local/sent delivery and optional tiny-reply drafts through the undo-send grace window | P2 | scaffolded | web, desktop, mobile |
| Per-account signatures | Rich-text signatures configured per connected account and applied automatically; `connected_accounts.signature_html`, auto-applied in compose and sent as real HTML | P2 | scaffolded | web, desktop, mobile |
| Attachment quick access | Search and browse all attachments from a conversation or contact via a command; `GET /v1/mail/attachments` (trigram filename search) + on-demand content fetch (Gmail 64MB cap, Graph) | P2 | scaffolded | web, desktop, mobile |
| **AI** | | | | |
| Ask AI | Natural-language questions over inbox and calendar with cited source emails, budget-gated; `POST /v1/ai/ask` (`ApiClient.aiAskCited`) is surfaced by a persistent sidebar (web), a thread-scoped dialog (desktop), and a modal (mobile) | P0 | scaffolded | web, desktop, mobile |
| Write with AI | Jot a few phrases and AI expands them into a full email in your voice; `POST /v1/ai/compose` via OpenRouter | P0 | scaffolded | web, desktop, mobile |
| Auto Drafts | AI proactively drafts replies, follow-ups, and scheduling responses before you open the email; background `auto_draft` AI job writes a provisional draft (`drafts.ai_generated`), surfaced with an AI-draft badge (Discard / Edit & send) | P0 | scaffolded | web, desktop, mobile |
| Instant Reply | Three precomputed, ready-to-send reply drafts under every conversation; `GET /v1/mail/threads/{id}/instant-replies` backed by the `instant_replies` AI job, cached on the thread row | P0 | scaffolded | web, desktop, mobile |
| Auto Summarize | Automatic one-line summary above every conversation, updating live as messages arrive; background `thread_summary` AI job writes `threads.summary`, refreshed on new inbound messages | P0 | scaffolded | web, desktop, mobile |
| Auto Labels | AI auto-categorizes incoming mail (marketing, pitches, social, news) for splitting and bulk processing; background `classify` AI job applies labels via user-defined classifiers | P1 | scaffolded | web, desktop, mobile |
| Custom Auto Labels | User-defined AI classifiers from short natural-language prompts, routed into dedicated splits; `ai_classifiers` CRUD (`/v1/classifiers`) feeds the `classify` AI job | P1 | scaffolded | web, desktop, mobile |
| Personal voice learning | AI learns tone, length, and structure from sent mail and adapts per recipient; background `voice_profile` AI job derives a per-user style profile from sent mail (30-day self-refresh) that feeds Auto Drafts and AI editing | P1 | scaffolded | web, desktop, mobile |
| AI editing commands | Improve, shorten, simplify, fix grammar, or change the tone of a draft with single AI commands; `POST /v1/ai/compose` (`improve\|shorten\|simplify\|fix_grammar\|change_tone`) via `ApiClient.aiEditDraft` | P1 | scaffolded | web, desktop, mobile |
| Auto Reminders | AI detects sent emails awaiting replies and sets follow-up reminders automatically; background `reminder_detect` AI job arms `remindAt` when unset | P1 | scaffolded | web, desktop, mobile |
| AI scheduling drafts | When someone asks to meet, AI drafts a reply pre-filled with real availability from your calendar; the `auto_draft` AI job checks calendar availability before drafting a scheduling reply | P1 | scaffolded | web, desktop, mobile |
| Ask AI sidebar | Persistent sidebar keeping Ask AI available for drafting, questions, and scheduling anywhere in the app; `AskSidebarProvider`/`AskSidebarPanel` toggled by `⌘J` and the command palette (web); desktop uses a thread-scoped Ask AI dialog instead | P2 | scaffolded | web, desktop |
| AI agent integrations | External agents (Claude, ChatGPT, EA workflows) drive Calendium and prepare drafts you review and send | P2 | planned | web, desktop |
| **Calendar-in-inbox** | | | | |
| Share Availability | Select free slots and insert them into an email as text plus a booking link; `GET /v1/availability` + compose flow | P0 | scaffolded | web, desktop, mobile |
| Calendar peek | See your day/week calendar without leaving the inbox (shortcut on desktop, swipe-down on mobile) | P1 | planned | web, desktop, mobile |
| One-tap event creation | Create an event from an email with title, attendees, location, and suggested time auto-filled from the thread | P1 | planned | web, desktop, mobile |
| Team scheduling | See when teammates are free and schedule group meetings from the inbox, no time-zone math; team availability + Find Time inline in compose over privacy-filtered busy blocks | P1 | scaffolded | web, desktop |
| Conferencing integrations | Zoom / Google Meet / Teams links attached automatically when creating meetings (`Conferencing` modeled on events) | P2 | planned | web, desktop, mobile |
| Time-zone aware scheduling | Availability and send-time suggestions account for the recipient's time zone | P2 | planned | web, desktop, mobile |
| **Collaboration** | | | | |
| Shared Conversations | Share a live, always-up-to-date view of any thread with teammates via link; `thread_shares` (token stored hash-only, fail-closed public view) + `/shared/{token}` web page streaming updates over SSE | P1 | scaffolded | web, desktop, mobile |
| Team Comments | Internal comments with @mentions on external threads, discussed without leaving email; `thread_comments` CRUD with team-scoped @mention resolution and mention notifications | P1 | scaffolded | web, desktop, mobile |
| Team read statuses and reply indicators | Read/replied state shared across the team on CC'd threads; correlated across member mailboxes via RFC Message-ID, honoring the per-member `share_read_statuses` opt-out | P1 | scaffolded | web, desktop, mobile |
| Team Snippets | Snippet templates shared across the team for consistent replies; team-scoped snippets via `snippets.team_id` | P2 | scaffolded | web, desktop, mobile |
| **Speed** | | | | |
| Sub-100ms interactions | Every interaction responds in under 100ms; served by the local Postgres mirror + optimistic mutations (architecture in place, perf budget enforced from M1; p95 measured via Playwright 36-46ms on production build) | P0 | scaffolded | web, desktop, mobile |
| Command palette (Cmd+K) | Search and execute any action from the keyboard, with each action's shortcut displayed to teach it | P0 | scaffolded | web, desktop |
| Comprehensive keyboard shortcuts | 100+ vim-inspired shortcuts covering every action (core j/k navigate + e archive set exists today; full coverage lands through M2) | P0 | scaffolded | web, desktop |
| Instant search | Locally indexed, as-you-type search over the mirror; `GET /v1/search` unified over threads + events | P0 | scaffolded | web, desktop, mobile |
| Offline mode | Read, search, and compose offline; queued messages send automatically when back online | P1 | shipped | web, desktop, mobile |
| Preloading architecture | Threads, images, and searches prefetched and cached before they're needed for zero perceived latency | P2 | shipped (web, desktop); mobile planned | web, desktop, mobile |
| Undo anything (Z) | Reverse nearly any action (archive, move, label) instantly, encouraging fearless fast triage | P2 | scaffolded | web, desktop, mobile |
| Shortcut teaching UX | UI surfaces the shortcut for every action taken via palette or mouse | P2 | scaffolded | web, desktop |
| Global desktop shortcuts | System-wide hotkeys open compose or search even when the app is in the background (Wails host) | P2 | shipped | desktop |
| **Platform** | | | | |
| Gmail and Outlook support | Full client on Gmail/Google Workspace and Outlook/Microsoft 365 (OAuth connect endpoints + provider ports modeled; live sync is M1) | P0 | planned | web, desktop, mobile |
| macOS and Windows desktop apps | Native-feeling Wails v2 desktop apps with the flagship keyboard-driven experience | P1 | planned | desktop |
| iOS app | Full-featured iPhone app with AI drafts, summaries, splits, swipe triage (Expo shell + Better Auth exists; product surface planned) | P1 | planned | mobile |
| Android app | Android app with splits, AI features, and fast triage (same Expo codebase) | P1 | planned | mobile |
| Multiple account switching | Connect several accounts and jump between them instantly with shortcuts (`ConnectedAccount` model supports many) | P1 | shipped | web, desktop, mobile |
| Concierge onboarding | White-glove 1:1 coaching session teaching shortcuts and reaching inbox zero; the in-app half is scaffolded — 8-step anchored guided tour (`onboarding-tour` over `data-tour` targets, localStorage `calendium.tour.v1`, palette "Restart tour") plus a shortcut coach toast after mouse-performed actions; the human 1:1 session is a business process, not code | P1 | scaffolded (in-app tour) | web, desktop |
| CRM integrations | HubSpot / Salesforce / Pipedrive records shown and updated from the inbox, auto-logged emails; `CrmProvider` port + HubSpot adapter (per-user OAuth via `integration_connections`) renders contact context in the contact pane and logs emails to the CRM — Salesforce/Pipedrive are deferred adapter drop-ins | P1 | scaffolded (HubSpot) | web, desktop |
| Attachment previews | PDFs preview inline; other file types open with system previews without leaving the inbox; web renders PDFs via a native `<iframe>` and images via `<img>` from the fetched attachment bytes, everything else downloads | P2 | scaffolded | web, desktop, mobile |
| Themes and dark mode | Multiple polished themes switchable from the palette (light+dark neutral token set plus named Ocean/Forest/Sunset palettes; per-user persistence via GET/PUT /v1/me/preferences with local offline fallback; pickers in the web command palette, desktop Settings, and mobile Settings) | P2 | scaffolded | web, desktop, mobile |
| Web app | Browser-based access to the full experience without installing the desktop client (Next.js `(app)` route group) | P2 | planned | web |

## Best-in-class calendar

| Feature | What it is | Priority | Status | Platforms |
| --- | --- | --- | --- | --- |
| **Creation** | | | | |
| Natural-language event parsing (Fantastical) | Typing "Lunch with Sarah at 1pm tomorrow" parses title, time, date, location, alerts, and recurrence live into a structured event | P0 | scaffolded | web, desktop, mobile |
| Event and task templates (Fantastical) | Reusable event/task templates (title, invitees, conferencing, alerts) to create recurring meeting types in one tap | P1 | scaffolded | web, desktop, mobile |
| NLP command bar for events (Vimcal) | A GPT-backed command bar turning free-form phrases into fully formed meetings with guests and conferencing | P1 | planned | web, desktop |
| Instant Event AI (Superhuman) | AI reads the email thread and proposes a ready-to-send event with title, attendees, and suggested time; `POST /v1/ai/event-proposal` feeds a review dialog before the real event-creation call | P1 | scaffolded | web, desktop, mobile |
| Auto events from Gmail (Google Calendar) | Flights, hotels, reservations detected in mail added to the calendar automatically with full details | P1 | planned | web, desktop, mobile |
| Siri / Apple Intelligence event creation (Apple Calendar) | Speak or type a natural phrase; people, dates, places extracted into an event | P2 | planned | mobile |
| Auto-detected events from Mail and Messages (Apple Calendar) | Flights, reservations, appointments found in messages suggested as events automatically | P2 | planned | mobile |
| **Views** | | | | |
| Full view range: day/week/month/quarter/year + DayTicker (Fantastical) | Polished day, week, month, quarter, year, and hybrid list/ticker views adapting across desktop and mobile | P0 | scaffolded | web, desktop, mobile |
| Multi-account overlay with cross-account blocking (Notion Calendar) | Overlay all work and personal accounts in one grid and block conflicts across accounts so double-booking is impossible | P0 | scaffolded | web, desktop, mobile |
| Calendar Sets (Fantastical) | Group calendars into named sets (work, home) that toggle together and switch automatically by time or location | P1 | scaffolded | web, desktop, mobile |
| Side-by-side calendar in inbox (Superhuman) | A mini day/week calendar opens beside the inbox to check and manage the schedule without leaving email | P1 | scaffolded | web, desktop |
| Keyword auto color-coding and time breakdown (Vimcal) | Events auto-colored by keyword rules and rolled up into a report of how time is actually spent | P2 | planned | web, desktop |
| Time Insights analytics (Google Calendar) | Panel breaking down meeting hours, most-met-with people, and the meetings-vs-focus split; `InsightsService.TimeInsights` (declined/cancelled/all-day/buffers excluded, 92-day cap) behind a right-side web panel with split bar, per-day minis, and top-people list | P2 | scaffolded | web, desktop |
| **Scheduling** | | | | |
| Drag-select availability sharing (Notion Calendar) | Drag open slots on the grid to generate a booking link plus a paste-ready text snippet of available times; backed by `GET /v1/availability` | P0 | scaffolded | web, desktop, mobile |
| Inline availability sharing from compose (Superhuman) | Cmd+Shift+A while writing an email, drag free slots, insert bookable times the recipient clicks to confirm; same availability backend | P0 | scaffolded | web, desktop |
| Openings booking pages (Fantastical) | Publish availability pages where others book time, with automatic conferencing attached to booked slots | P0 | planned | web |
| Personalized booking links (Vimcal) | Customizable Calendly-style booking links created in seconds from inside the calendar | P0 | planned | web, desktop, mobile |
| Appointment schedules (Google Calendar) | Native booking page with custom availability windows, buffer times, and daily booking limits writing directly to the calendar | P0 | planned | web |
| Meeting Proposals / polls (Fantastical) | Propose multiple candidate times invitees vote on, then confirm the winner onto everyone's calendar | P1 | planned | web, desktop, mobile |
| Drag-and-copy availability as text (Vimcal) | Drag free slots and copy them as formatted plain-text times auto-converted to the recipient's zone | P1 | planned | web, desktop |
| Booking pages (Superhuman) | Standing personal booking pages for recurring meeting types, managed inside the email client | P1 | planned | web, desktop |
| AI scheduling engine (Rise) | Hundreds of signals propose best meeting times, resolve conflicts, and reshuffle events automatically | P1 | planned | web, desktop, mobile |
| FocusGuard automatic focus blocking (Rise) | Set a minimum weekly focus-time goal; the calendar automatically blocks and defends deep-work time; weekly focus-goal automation prefs + worker planning of `ManagedFocus` blocks into free gaps (`managed_events`, migration 0022) | P1 | scaffolded | web, desktop, mobile |
| Focus Time with auto-decline (Google Calendar) | Focus blocks signal unavailability, auto-decline conflicting invites, silence notifications; focus and OOO auto-decline of conflicting invites with a custom RSVP message, gated by automation prefs | P1 | scaffolded | web, desktop, mobile |
| AI task slotting (Amie) | AI suggests the best open slots for unscheduled todos based on schedule and energy of the day | P2 | planned | web, desktop, mobile |
| Auto buffers between meetings (Rise) | Automatically inserts breathing room between back-to-back meetings for preparation and recovery; worker inserts `ManagedBuffer` events between back-to-back meetings per the buffer-minutes automation pref | P2 | scaffolded | web, desktop, mobile |
| **Time zones** | | | | |
| Multiple time zones on the grid (Notion Calendar) | Pin several time zones as parallel columns on the week grid to reason about distributed-team hours | P0 | scaffolded | web, desktop |
| Time Travel timezone comparison (Vimcal) | Temporarily overlay any city's timezone on the calendar to find globally workable slots, DST handled automatically | P1 | planned | web, desktop, mobile |
| Secondary time zone display (Google Calendar) | Labeled second time zone alongside the primary axis plus a world-clock widget | P1 | planned | web, desktop |
| Recipient-time-zone preview (Vimcal) | Shared slots and booking pages render automatically in the recipient's local time zone, eliminating conversion errors | P2 | planned | web, desktop |
| **Collaboration** | | | | |
| Shared calendars with granular permissions (Google Calendar) | Per-person sharing with tiered permissions from free/busy-only up to full edit and delegation; `calendar_shares` with free-busy/reader tiers, server-side busy-only field redaction, audited grants (local layer — provider ACLs out of scope, see `docs/collaboration.md`) | P0 | scaffolded | web, desktop, mobile |
| Find a Time guest availability grid (Google Calendar) | View guests' free/busy side by side when inviting and pick a slot that works for everyone; side-by-side member busy grid from the team availability endpoint | P0 | scaffolded | web, desktop, mobile |
| Find Time inline team availability (Superhuman) | Teammates' availability inline while composing; share only slots that work for the group, or override deliberately; inline compose surface over the team availability API | P1 | scaffolded | web, desktop |
| Team availability overview (Rise) | Every teammate's availability and time zone at a glance to coordinate without asking around; privacy-filtered opaque busy blocks, 35-day window cap | P1 | scaffolded | web, desktop |
| Team scheduling links (Rise) | Scheduling links checking one or multiple team members' calendars so externals book the whole group at once; `booking_links.team_id` + `booking_link_members`, collective all-free mode (round-robin is a noted follow-up) | P1 | scaffolded | web |
| Propose-new-time RSVP flow (Google Calendar) | Invitees respond Yes/No/Maybe with a note or counter-propose a time the organizer accepts in one click | P1 | planned | web, desktop, mobile |
| Working location and hours (Google Calendar) | Broadcast where you work (office, home) and working hours so colleagues schedule appropriately | P1 | planned | web, desktop, mobile |
| Out-of-office auto-decline (Google Calendar) | OOO blocks automatically decline incoming and existing meetings during the away period with a custom message | P2 | planned | web, desktop, mobile |
| EA delegation mode (Vimcal) | Executive assistants manage multiple executives' calendars from one interface with calendar holds and scheduling analytics; `delegations` grants + act-as request scoping with append-only `audit_entries` attribution | P2 | scaffolded | web, desktop |
| **Speed** | | | | |
| Sub-100ms speed with a shortcut for everything (Vimcal) | Every calendar action, from event creation to jumping between meetings, has a shortcut and responds in under 100ms | P0 | planned | web, desktop |
| Keyboard-first navigation (Notion Calendar) | Single-key shortcuts (T today, J/K move, S share availability, ? shortcut list) drive the whole app | P1 | scaffolded | web, desktop |
| Command menu / Cmd+K (Notion Calendar) | Palette to jump to dates, switch views, and trigger any calendar action without the mouse (unified with the mail palette) | P1 | scaffolded | web, desktop |
| Keyboard-first calendar control in email (Superhuman) | All calendar actions (view, share, create, RSVP) reachable via shortcuts inside the mail workflow with no context switch | P1 | scaffolded | web, desktop |
| Menu-bar mini calendar (Fantastical) | Full mini calendar in the macOS menu bar for glance-and-create access without opening the main app; shipped in TEXT form for now (today's remaining events as native tray menu rows via `energye/systray`) — the graphical NSPopover month calendar is not possible in Wails v2 (single-window, native text-only tray menus) and is deferred to the Wails v3 migration | P1 | scaffolded (text), graphical deferred to Wails v3 | desktop |
| Menu-bar next event + instant join (Notion Calendar) | Menu-bar countdown to your next meeting with one-click call join from anywhere (tray title countdown + Join row; frontend pushes the next-12h feed via `SetUpcomingEvents`, join URLs from shared `detectConference`) | P1 | scaffolded | desktop |
| Auto-join meetings (Rise) | The app opens the video call for you at start time so no one is late hunting for the link (host-side scheduler fires at start−lead when the user's Settings toggle is on; 0/30/60s lead, exactly-once per event, "Joined …" toast) | P2 | scaffolded | desktop |
| **Integrations** | | | | |
| Conference-call detection with join button (Fantastical) | Detects Zoom/Meet/Teams/Webex links in any event and surfaces a one-click Join button at meeting time (`Conferencing` modeled on `Event`); web renders it on time-grid/month-popover/ticker event surfaces and in the event dialog via shared `detectConference`/`isJoinable` | P0 | scaffolded | web, desktop, mobile |
| One-click add video conferencing (Fantastical) | Attach a Zoom/Meet/Teams/Webex room to any event during creation without leaving the app (`addConferencing` in `EventInput`); web's event dialog offers this only on create — `EventPatch` has no conferencing field, so the backend can attach a link once at creation but not toggle it via PATCH on an existing event | P1 | scaffolded | web, desktop, mobile |
| Automatic Google Meet attachment (Google Calendar) | Every event with guests gets a Meet link attached by default | P1 | planned | web, desktop, mobile |
| Tasks inline with events (Fantastical) | Reminders/Todoist tasks render in the calendar timeline next to events and complete in place; first-class task domain (`/v1/tasks`, migration 0019) + Todoist-synced tasks in the calendar task rail, check-off in place | P1 | scaffolded | web, desktop, mobile |
| Todos and events in one timeline (Amie) | Tasks live beside events and drag onto the grid to become time blocks; web task rail beside the calendar grid with drag-to-timeblock (15-min snapping) and quick-add, mirrored task surfaces on mobile/desktop | P1 | scaffolded | web, desktop, mobile |
| Tasks on the calendar grid (Google Calendar) | Tasks with a date and time appear as schedulable blocks on the calendar and check off in place; scheduled tasks render as grid timeblocks and complete in place from rail or block | P1 | scaffolded | web, desktop, mobile |
| Docs attached to events (Notion Calendar) | Link pages and meeting notes directly to events so context and agenda travel with the meeting; per-event notes + doc links (`event_notes`, migration 0020) editable from the event dialog | P1 | scaffolded | web, desktop, mobile |
| Travel time with time-to-leave alerts (Apple Calendar) | Location-aware travel time added to events with leave-now alerts based on live traffic and transit; OSRM-routed travel-time buffer events before geocoded events plus leave-now push alerts (`travel_alerts`, migration 0025) — live traffic/transit awaits a paid maps adapter | P1 | scaffolded | mobile, desktop |
| Reminders unified in calendar (Apple Calendar) | Create, view, and complete reminder tasks directly inside day/week/month views | P1 | planned | web, desktop, mobile |
| Email-to-event drag and drop (Amie) | Drag emails from the inbox onto the calendar or task list to turn messages into scheduled work; thread rows drag onto the calendar grid or peek (`application/x-calendium-thread`) to open a prefilled EventDialog with attendees and a thread reference | P2 | scaffolded | web, desktop |
| Built-in Pomodoro focus timer (Amie) | Native Pomodoro timer running against calendar blocks for distraction-free deep work | P2 | planned | web, desktop, mobile |
| Weather in calendar (Fantastical) | Inline multi-day forecast displayed alongside days so scheduling accounts for conditions; keyless Open-Meteo `WeatherProvider` feeds inline day forecasts, opt-in via the `weatherEnabled` calendar pref | P2 | scaffolded | web, desktop, mobile |
| Interesting Calendars subscriptions (Fantastical) | One-tap subscriptions to curated public calendars (sports, TV schedules, holidays); ICS feed subscriptions (`calendar_subscriptions`, migration 0026) refreshed hourly through the stdlib RFC 5545 parser (`internal/ics`) with bounded RRULE expansion | P2 | scaffolded | web, desktop, mobile |
| External todo tool integrations (Amie) | Tasks from Todoist, Things 3, Notion, Linear pulled into the calendar so all work is schedulable in one view; `TodoProvider` port with a Todoist adapter (per-user OAuth, incremental worker sync, two-way completion) — Things 3/Notion/Linear are deferred adapter drop-ins | P2 | scaffolded (Todoist) | web, desktop |
| Maps-powered locations (Apple Calendar) | Location autocomplete with map preview, enabling directions and travel-time features on any event; `MapsProvider` port with a keyless Nominatim/OSRM adapter behind `/v1/places` autocomplete, storing lat/lng on events (migration 0024) — paid Google/Apple adapters are config drop-ins later | P2 | scaffolded | web, desktop, mobile |
| Scheduled FaceTime calls (Apple Calendar) | Events that are themselves a FaceTime call with native one-tap join for Apple-device invitees | P2 | planned | mobile |
| Delight timeline extras (Amie) | e.g. Spotify listening history logged onto the day as a personal record of time | P2 | planned | web, desktop, mobile |
| Deep OS integration: widgets, watch, lock screen (Apple Calendar) | Calendar surfaces across the ecosystem via widgets, watch complications, and lock-screen glances | P2 | planned | mobile |

## Roadmap

### M0 — This scaffold (now)

- Bun monorepo with `apps/*` + `packages/*` workspaces; contracts locked in `docs/architecture.md`, `docs/tech-stack.md`, `docs/payments.md`.
- `packages/shared`: full TypeScript domain model + typed `ApiClient` mirroring the Go domain and REST v1 contract.
- Go backend skeleton (hexagonal, stdlib-only): REST v1 surface, Better Auth JWT verification (EdDSA/Ed25519 via JWKS), Stripe $50/yr checkout/portal/webhooks, OpenRouter AI adapter, APNs/FCM/WebPush adapters, Postgres migrations.
- Product surface scaffolded end to end (mock/local data where provider sync is not yet live): split inbox model, thread list with j/k/e shortcuts, Cmd+K command palette, snooze, send later, drafts management (list/open/delete/update via `ApiClient`), starred/snoozed/sent pseudo-views (`?view=`), follow-up reminders, snippets, AI compose/summarize/ask via OpenRouter, read-state sync (`markThreadOpened`), VIP senders, unified search, undo-send with a post-send Undo toast (`undoSendSeconds` from `GET /v1/instance`), web push registration (VAPID), and share availability.
- Expo mobile shell with Better Auth (email+password + Google/Apple sign-in, deep linking) and the reader-mode payments treatment (no IAP).
- Wails desktop client reaching the scaffolded surface — compose, unified search, and basic calendar event CRUD — with runtime server switching, plus Google/Apple sign-in handed off through the system browser via one-time codes (`/desktop-callback` → `calendium://auth/callback?ott=…`).
- One design system everywhere: shadcn/ui new-york, neutral HSL tokens, radius 0.625rem, lucide icons, light + dark.

### M1 — Live provider sync

- Google + Microsoft OAuth connect flows with AES-GCM-encrypted refresh tokens; account management UI on all platforms.
- `cmd/worker` incremental sync: Gmail `historyId` and Microsoft Graph delta queries mirroring threads, messages, labels, calendars, and events into Postgres; mutations write through to providers with optimistic local updates.
- Split-inbox classification at ingest (header heuristics + optional OpenRouter pass) running against real mail.
- Real send pipeline: scheduled send (Send Later), server-side undo-send hold, snooze and follow-up-reminder wake-up loops.
- Read-status tracking (open events) wired to `Message.openedAt`.
- Calendar CRUD/RSVP through providers; availability computed from real free/busy; booking-confirmation flow for shared slots.
- Locally indexed instant search over the mirror; sub-100ms interaction budget measured and enforced.
- Push notifications live: APNs, FCM, Web Push for important mail, event reminders, and wake-ups.
- Billing enforcement live: 14-day trial, webhook-driven subscription state, paywall on web/desktop, reader-mode state on mobile.
- Web (Next.js `(app)`) and desktop (Wails) clients reach the scaffolded feature set on live data; mobile gains swipe triage, splits, and compose.

### M2 — Full parity

- **AI suite**: Ask AI with cited sources (+ persistent sidebar), Auto Drafts, Instant Reply, AI editing commands, Auto Labels + custom natural-language classifiers, personal voice learning, Auto Reminders, AI scheduling drafts, Instant Event AI, external AI-agent integrations.
- **Triage power tools**: Get Me To Zero, bulk triage, one-click/bulk unsubscribe, auto-advance, reorderable splits, contact pane with social insights, inbox-zero celebration, stars/labels fully keyboard-driven.
- **Compose extras**: Smart Send, Instant Intro, Recent Opens feed, Auto Bcc, emoji reactions, per-account signatures, attachment quick access.
- **Collaboration**: shared conversations, team comments with @mentions, team read statuses, team snippets; calendar-side team availability, team scheduling links, Find Time inline.
- **Calendar best-of-breed**: natural-language event parsing + NLP command bar, booking pages/links + appointment schedules, meeting polls, event templates, calendar sets, full view range (day→year + ticker), multi-account overlay with cross-account blocking, multi-timezone grid + Time Travel + recipient-TZ preview, conferencing auto-attach with join buttons and auto-join, tasks/todos on the grid + external todo tools, docs on events, travel time + Maps locations, auto buffers, focus-time auto-decline + FocusGuard, working hours/location, propose-new-time RSVP, OOO auto-decline, EA delegation mode, time analytics, weather, subscriptions, delight extras.
- **Platform polish**: offline mode with queued send, preloading architecture, undo-anything (Z), 100+ shortcut coverage with shortcut-teaching UX, global desktop shortcuts, menu-bar calendar + next-event join, attachment previews, mobile widgets/watch/lock-screen surfaces, multiple named themes, multi-account switching shortcuts, CRM integrations, concierge onboarding.
