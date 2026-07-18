package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Compile-time proof every double satisfies its port.
var (
	_ port.TokenVerifier   = (*fakeVerifier)(nil)
	_ port.UserService     = (*fakeUserService)(nil)
	_ port.BillingService  = (*fakeBillingService)(nil)
	_ port.AccountService  = (*fakeAccountService)(nil)
	_ port.MailService     = (*fakeMailService)(nil)
	_ port.CalendarService = (*fakeCalendarService)(nil)
	_ port.SearchService   = (*fakeSearchService)(nil)
	_ port.AIService       = (*fakeAIService)(nil)
	_ port.DeviceService   = (*fakeDeviceService)(nil)
	_ port.PrefsService    = (*fakePrefsService)(nil)

	_ port.SchedulingService = (*fakeSchedulingService)(nil)
	_ port.SettingsService   = (*fakeSettingsService)(nil)
)

const (
	defaultToken  = "valid-token"
	defaultUserID = "user_1"
)

// --- token verifier ----------------------------------------------------------

type fakeVerifier struct {
	tokens map[string]port.Identity // token => identity
	err    error                    // when non-nil, Verify always fails
	calls  int
	gotJWT string
}

func (f *fakeVerifier) Verify(ctx context.Context, jwt string) (port.Identity, error) {
	f.calls++
	f.gotJWT = jwt
	if f.err != nil {
		return port.Identity{}, f.err
	}
	id, ok := f.tokens[jwt]
	if !ok {
		return port.Identity{}, domain.ErrUnauthorized
	}
	return id, nil
}

// --- UserService -------------------------------------------------------------

type fakeUserService struct {
	ensureRet   domain.User
	ensureErr   error
	ensureCalls int
	gotIdentity port.Identity

	getRet domain.User
	getErr error
}

func (f *fakeUserService) EnsureUser(ctx context.Context, id port.Identity) (domain.User, error) {
	f.ensureCalls++
	f.gotIdentity = id
	return f.ensureRet, f.ensureErr
}
func (f *fakeUserService) GetUser(ctx context.Context, userID string) (domain.User, error) {
	return f.getRet, f.getErr
}

// --- BillingService ----------------------------------------------------------

type fakeBillingService struct {
	subRet domain.Subscription
	subErr error

	checkoutURL           string
	checkoutErr           error
	gotCheckoutSuccessURL string
	gotCheckoutCancelURL  string

	portalURL       string
	portalErr       error
	gotPortalReturn string

	webhookErr        error
	webhookCalls      int
	gotWebhookPayload []byte
	gotWebhookSig     string

	requireActiveErr error
}

func (f *fakeBillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	return f.subRet, f.subErr
}
func (f *fakeBillingService) CreateCheckoutSession(ctx context.Context, userID, successURL, cancelURL string) (string, error) {
	f.gotCheckoutSuccessURL, f.gotCheckoutCancelURL = successURL, cancelURL
	return f.checkoutURL, f.checkoutErr
}
func (f *fakeBillingService) CreatePortalSession(ctx context.Context, userID, returnURL string) (string, error) {
	f.gotPortalReturn = returnURL
	return f.portalURL, f.portalErr
}
func (f *fakeBillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	f.webhookCalls++
	f.gotWebhookPayload = payload
	f.gotWebhookSig = sigHeader
	return f.webhookErr
}
func (f *fakeBillingService) RequireActive(ctx context.Context, userID string) error {
	return f.requireActiveErr
}

// --- AccountService ----------------------------------------------------------

type fakeAccountService struct {
	listRet []domain.ConnectedAccount
	listErr error

	beginURL        string
	beginErr        error
	gotProvider     domain.Provider
	gotRedirectURL  string
	gotBeginBaseURL string

	completeAccount  domain.ConnectedAccount
	completeRedirect string
	completeErr      error
	gotState         string
	gotCode          string

	vipRet   domain.ConnectedAccount
	vipErr   error
	gotVIPID string
	gotVIP   []string

	disconnectErr   error
	gotDisconnectID string

	sigRet   domain.ConnectedAccount
	sigErr   error
	gotSigID string
	gotSig   string

	autoBccRet   domain.ConnectedAccount
	autoBccErr   error
	gotAutoBccID string
	gotAutoBcc   []string
}

func (f *fakeAccountService) List(ctx context.Context, userID string) ([]domain.ConnectedAccount, error) {
	return f.listRet, f.listErr
}
func (f *fakeAccountService) BeginConnect(ctx context.Context, userID string, provider domain.Provider, redirectURL, requestBaseURL string) (string, error) {
	f.gotProvider, f.gotRedirectURL, f.gotBeginBaseURL = provider, redirectURL, requestBaseURL
	return f.beginURL, f.beginErr
}
func (f *fakeAccountService) CompleteConnect(ctx context.Context, provider domain.Provider, state, code, requestBaseURL string) (domain.ConnectedAccount, string, error) {
	f.gotProvider, f.gotState, f.gotCode = provider, state, code
	return f.completeAccount, f.completeRedirect, f.completeErr
}
func (f *fakeAccountService) SetVipSenders(ctx context.Context, userID, accountID string, vipSenders []string) (domain.ConnectedAccount, error) {
	f.gotVIPID, f.gotVIP = accountID, vipSenders
	return f.vipRet, f.vipErr
}
func (f *fakeAccountService) Disconnect(ctx context.Context, userID, accountID string) error {
	f.gotDisconnectID = accountID
	return f.disconnectErr
}
func (f *fakeAccountService) SetSignature(ctx context.Context, userID, accountID, signatureHTML string) (domain.ConnectedAccount, error) {
	f.gotSigID, f.gotSig = accountID, signatureHTML
	return f.sigRet, f.sigErr
}
func (f *fakeAccountService) SetAutoBcc(ctx context.Context, userID, accountID string, autoBcc []string) (domain.ConnectedAccount, error) {
	f.gotAutoBccID, f.gotAutoBcc = accountID, autoBcc
	return f.autoBccRet, f.autoBccErr
}

// --- MailService -------------------------------------------------------------

type fakeMailService struct {
	// ListThreads
	listPage      domain.Page[domain.Thread]
	listErr       error
	listCalls     int
	gotListUserID string
	gotListQuery  port.ThreadQuery

	// GetThread
	getThreadThread domain.Thread
	getThreadMsgs   []domain.Message
	getThreadErr    error
	gotGetThreadID  string

	// ActOnThread
	actRet    domain.Thread
	actErr    error
	gotAction domain.ThreadAction
	gotActID  string

	markOpenedErr error
	gotMarkID     string

	snoozeRet   domain.Thread
	snoozeErr   error
	gotSnoozeID string
	gotUntil    time.Time

	unsnoozeRet   domain.Thread
	unsnoozeErr   error
	gotUnsnoozeID string

	// BulkActOnThreads
	bulkResult    port.BulkActionResult
	bulkErr       error
	bulkUserID    string
	bulkThreadIDs []string
	bulkAction    domain.ThreadAction

	reminderRet   domain.Thread
	reminderErr   error
	gotReminderID string
	gotRemindAt   *time.Time

	// ArchiveOlderThan
	zeroCount     int
	zeroErr       error
	zeroUserID    string
	zeroOlderThan time.Time

	// drafts
	createDraftRet domain.Draft
	createDraftErr error
	gotCreateDraft port.DraftInput

	updateDraftRet domain.Draft
	updateDraftErr error
	gotUpdateDraft port.DraftInput

	getDraftRet domain.Draft
	getDraftErr error

	listDraftsRet []domain.Draft
	listDraftsErr error

	deleteDraftErr error
	gotDeleteDraft string

	sendDraftRet domain.Message
	sendDraftErr error

	unsendDraftRet domain.Draft
	unsendDraftErr error

	// snippets
	listSnippetsRet []domain.Snippet
	listSnippetsErr error
	createSnippet   domain.Snippet
	createSnipErr   error
	updateSnippet   domain.Snippet
	updateSnipErr   error
	deleteSnipErr   error

	// ListLabels
	listLabelsRet []domain.Label
	listLabelsErr error

	// SetThreadLabel
	setLabelRet        domain.Thread
	setLabelErr        error
	gotSetLabelID      string
	gotSetLabelLabelID string
	gotSetLabelAdd     bool

	// BulkSetLabel
	bulkSetLabelRet        port.BulkActionResult
	bulkSetLabelErr        error
	gotBulkSetLabelUserID  string
	gotBulkSetLabelIDs     []string
	gotBulkSetLabelLabelID string
	gotBulkSetLabelAdd     bool

	// UnsubscribeThread
	unsubscribeRet   port.UnsubscribeResult
	unsubscribeErr   error
	gotUnsubscribeID string

	// M2.5: opens feed, smart send, attachments, contact, reactions
	listOpensRet     domain.Page[domain.OpenEvent]
	listOpensErr     error
	gotListOpensUser string
	gotListOpensCur  string
	gotListOpensLim  int

	suggestSendRet     domain.SendSuggestion
	suggestSendErr     error
	gotSuggestSendUser string
	gotSuggestSendMail string

	searchAttachmentsRet     domain.Page[domain.AttachmentHit]
	searchAttachmentsErr     error
	gotSearchAttachmentsUser string
	gotSearchAttachmentsQ    port.AttachmentQuery

	getAttachmentContentData     []byte
	getAttachmentContentMimeType string
	getAttachmentContentFilename string
	getAttachmentContentErr      error
	gotGetAttachmentContentUser  string
	gotGetAttachmentContentID    string

	getContactRet     domain.ContactSummary
	getContactErr     error
	gotGetContactUser string
	gotGetContactMail string

	reactRet          port.ReactionResult
	reactErr          error
	gotReactUser      string
	gotReactMessageID string
	gotReactEmoji     string
	gotReactSendReply bool

	removeReactionErr          error
	gotRemoveReactionUser      string
	gotRemoveReactionMessageID string
	gotRemoveReactionEmoji     string
}

func (f *fakeMailService) ListThreads(ctx context.Context, userID string, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	f.listCalls++
	f.gotListUserID = userID
	f.gotListQuery = q
	return f.listPage, f.listErr
}
func (f *fakeMailService) GetThread(ctx context.Context, userID, threadID string) (domain.Thread, []domain.Message, error) {
	f.gotGetThreadID = threadID
	return f.getThreadThread, f.getThreadMsgs, f.getThreadErr
}
func (f *fakeMailService) ActOnThread(ctx context.Context, userID, threadID string, action domain.ThreadAction) (domain.Thread, error) {
	f.gotActID, f.gotAction = threadID, action
	return f.actRet, f.actErr
}
func (f *fakeMailService) MarkThreadOpened(ctx context.Context, userID, threadID string) error {
	f.gotMarkID = threadID
	return f.markOpenedErr
}
func (f *fakeMailService) SnoozeThread(ctx context.Context, userID, threadID string, until time.Time) (domain.Thread, error) {
	f.gotSnoozeID, f.gotUntil = threadID, until
	return f.snoozeRet, f.snoozeErr
}
func (f *fakeMailService) UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error) {
	f.gotUnsnoozeID = threadID
	return f.unsnoozeRet, f.unsnoozeErr
}
func (f *fakeMailService) BulkActOnThreads(ctx context.Context, userID string, threadIDs []string, action domain.ThreadAction) (port.BulkActionResult, error) {
	f.bulkUserID, f.bulkThreadIDs, f.bulkAction = userID, threadIDs, action
	return f.bulkResult, f.bulkErr
}
func (f *fakeMailService) SetReminder(ctx context.Context, userID, threadID string, remindAt *time.Time) (domain.Thread, error) {
	f.gotReminderID, f.gotRemindAt = threadID, remindAt
	return f.reminderRet, f.reminderErr
}
func (f *fakeMailService) ArchiveOlderThan(ctx context.Context, userID string, olderThan time.Time) (int, error) {
	f.zeroUserID, f.zeroOlderThan = userID, olderThan
	return f.zeroCount, f.zeroErr
}
func (f *fakeMailService) CreateDraft(ctx context.Context, userID string, in port.DraftInput) (domain.Draft, error) {
	f.gotCreateDraft = in
	return f.createDraftRet, f.createDraftErr
}
func (f *fakeMailService) UpdateDraft(ctx context.Context, userID, draftID string, in port.DraftInput) (domain.Draft, error) {
	f.gotUpdateDraft = in
	return f.updateDraftRet, f.updateDraftErr
}
func (f *fakeMailService) GetDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	return f.getDraftRet, f.getDraftErr
}
func (f *fakeMailService) ListDrafts(ctx context.Context, userID string) ([]domain.Draft, error) {
	return f.listDraftsRet, f.listDraftsErr
}
func (f *fakeMailService) DeleteDraft(ctx context.Context, userID, draftID string) error {
	f.gotDeleteDraft = draftID
	return f.deleteDraftErr
}
func (f *fakeMailService) SendDraft(ctx context.Context, userID, draftID string) (domain.Message, error) {
	return f.sendDraftRet, f.sendDraftErr
}
func (f *fakeMailService) UnsendDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	return f.unsendDraftRet, f.unsendDraftErr
}
func (f *fakeMailService) ListSnippets(ctx context.Context, userID string) ([]domain.Snippet, error) {
	return f.listSnippetsRet, f.listSnippetsErr
}
func (f *fakeMailService) CreateSnippet(ctx context.Context, userID string, in port.SnippetInput) (domain.Snippet, error) {
	return f.createSnippet, f.createSnipErr
}
func (f *fakeMailService) UpdateSnippet(ctx context.Context, userID, snippetID string, in port.SnippetInput) (domain.Snippet, error) {
	return f.updateSnippet, f.updateSnipErr
}
func (f *fakeMailService) DeleteSnippet(ctx context.Context, userID, snippetID string) error {
	return f.deleteSnipErr
}
func (f *fakeMailService) ListLabels(ctx context.Context, userID string) ([]domain.Label, error) {
	return f.listLabelsRet, f.listLabelsErr
}
func (f *fakeMailService) SetThreadLabel(ctx context.Context, userID, threadID, labelID string, add bool) (domain.Thread, error) {
	f.gotSetLabelID, f.gotSetLabelLabelID, f.gotSetLabelAdd = threadID, labelID, add
	return f.setLabelRet, f.setLabelErr
}
func (f *fakeMailService) BulkSetLabel(ctx context.Context, userID string, threadIDs []string, labelID string, add bool) (port.BulkActionResult, error) {
	f.gotBulkSetLabelUserID, f.gotBulkSetLabelIDs, f.gotBulkSetLabelLabelID, f.gotBulkSetLabelAdd = userID, threadIDs, labelID, add
	return f.bulkSetLabelRet, f.bulkSetLabelErr
}
func (f *fakeMailService) UnsubscribeThread(ctx context.Context, userID, threadID string) (port.UnsubscribeResult, error) {
	f.gotUnsubscribeID = threadID
	return f.unsubscribeRet, f.unsubscribeErr
}
func (f *fakeMailService) ListOpens(ctx context.Context, userID, cursor string, limit int) (domain.Page[domain.OpenEvent], error) {
	f.gotListOpensUser, f.gotListOpensCur, f.gotListOpensLim = userID, cursor, limit
	return f.listOpensRet, f.listOpensErr
}
func (f *fakeMailService) SuggestSendTime(ctx context.Context, userID, recipientEmail string) (domain.SendSuggestion, error) {
	f.gotSuggestSendUser, f.gotSuggestSendMail = userID, recipientEmail
	return f.suggestSendRet, f.suggestSendErr
}
func (f *fakeMailService) SearchAttachments(ctx context.Context, userID string, q port.AttachmentQuery) (domain.Page[domain.AttachmentHit], error) {
	f.gotSearchAttachmentsUser, f.gotSearchAttachmentsQ = userID, q
	return f.searchAttachmentsRet, f.searchAttachmentsErr
}
func (f *fakeMailService) GetAttachmentContent(ctx context.Context, userID, attachmentID string) ([]byte, string, string, error) {
	f.gotGetAttachmentContentUser, f.gotGetAttachmentContentID = userID, attachmentID
	return f.getAttachmentContentData, f.getAttachmentContentMimeType, f.getAttachmentContentFilename, f.getAttachmentContentErr
}
func (f *fakeMailService) GetContact(ctx context.Context, userID, email string) (domain.ContactSummary, error) {
	f.gotGetContactUser, f.gotGetContactMail = userID, email
	return f.getContactRet, f.getContactErr
}
func (f *fakeMailService) ReactToMessage(ctx context.Context, userID, messageID, emoji string, sendReply bool) (port.ReactionResult, error) {
	f.gotReactUser, f.gotReactMessageID, f.gotReactEmoji, f.gotReactSendReply = userID, messageID, emoji, sendReply
	return f.reactRet, f.reactErr
}
func (f *fakeMailService) RemoveReaction(ctx context.Context, userID, messageID, emoji string) error {
	f.gotRemoveReactionUser, f.gotRemoveReactionMessageID, f.gotRemoveReactionEmoji = userID, messageID, emoji
	return f.removeReactionErr
}

// --- CalendarService ---------------------------------------------------------

type fakeCalendarService struct {
	listCalsRet []domain.Calendar
	listCalsErr error

	updateCalRet domain.Calendar
	updateCalErr error
	gotUpdateCal port.CalendarPatch

	listEventsRet   []domain.Event
	listEventsErr   error
	listEventsCalls int
	gotEventsFrom   time.Time
	gotEventsTo     time.Time
	gotEventsCalIDs []string

	createEventRet domain.Event
	createEventErr error

	updateEventRet domain.Event
	updateEventErr error

	deleteEventErr error
	gotDeleteEvt   string

	rsvpRet   domain.Event
	rsvpErr   error
	gotRsvp   domain.RsvpStatus
	gotRsvpID string

	availRet    []domain.AvailabilitySlot
	availErr    error
	availCalls  int
	gotAvailDur time.Duration

	// Event Template methods
	listTemplatesRet   []domain.EventTemplate
	listTemplatesErr   error
	listTemplatesCalls int

	createTemplateRet    domain.EventTemplate
	createTemplateErr    error
	gotCreateTemplate    domain.EventTemplateInput
	gotCreateTemplUserID string

	updateTemplateRet domain.EventTemplate
	updateTemplateErr error
	gotUpdateTemplate domain.EventTemplateInput
	gotUpdateTemplID  string

	deleteTemplateErr error
	gotDeleteTemplID  string

	useTemplateErr   error
	gotUseTemplID    string
	useTemplateCalls int

	// Calendar Set methods
	listSetsRet   []domain.CalendarSet
	listSetsErr   error
	listSetsCalls int

	createSetRet       domain.CalendarSet
	createSetErr       error
	gotCreateSet       domain.CalendarSetInput
	gotCreateSetUserID string

	updateSetRet   domain.CalendarSet
	updateSetErr   error
	gotUpdateSet   domain.CalendarSetInput
	gotUpdateSetID string

	deleteSetErr   error
	gotDeleteSetID string
}

func (f *fakeCalendarService) ListCalendars(ctx context.Context, userID string) ([]domain.Calendar, error) {
	return f.listCalsRet, f.listCalsErr
}
func (f *fakeCalendarService) UpdateCalendar(ctx context.Context, userID, calendarID string, patch port.CalendarPatch) (domain.Calendar, error) {
	f.gotUpdateCal = patch
	return f.updateCalRet, f.updateCalErr
}
func (f *fakeCalendarService) ListEvents(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	f.listEventsCalls++
	f.gotEventsFrom, f.gotEventsTo, f.gotEventsCalIDs = from, to, calendarIDs
	return f.listEventsRet, f.listEventsErr
}
func (f *fakeCalendarService) CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	return f.createEventRet, f.createEventErr
}
func (f *fakeCalendarService) UpdateEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error) {
	return f.updateEventRet, f.updateEventErr
}
func (f *fakeCalendarService) DeleteEvent(ctx context.Context, userID, eventID string) error {
	f.gotDeleteEvt = eventID
	return f.deleteEventErr
}
func (f *fakeCalendarService) RSVP(ctx context.Context, userID, eventID string, response domain.RsvpStatus) (domain.Event, error) {
	f.gotRsvpID, f.gotRsvp = eventID, response
	return f.rsvpRet, f.rsvpErr
}
func (f *fakeCalendarService) Availability(ctx context.Context, userID string, from, to time.Time, slotDuration time.Duration) ([]domain.AvailabilitySlot, error) {
	f.availCalls++
	f.gotEventsFrom, f.gotEventsTo, f.gotAvailDur = from, to, slotDuration
	return f.availRet, f.availErr
}

// --- Event Template Methods

func (f *fakeCalendarService) ListEventTemplates(ctx context.Context, userID string) ([]domain.EventTemplate, error) {
	f.listTemplatesCalls++
	return f.listTemplatesRet, f.listTemplatesErr
}

func (f *fakeCalendarService) CreateEventTemplate(ctx context.Context, userID string, in domain.EventTemplateInput) (domain.EventTemplate, error) {
	f.gotCreateTemplate = in
	f.gotCreateTemplUserID = userID
	return f.createTemplateRet, f.createTemplateErr
}

func (f *fakeCalendarService) UpdateEventTemplate(ctx context.Context, userID, templateID string, in domain.EventTemplateInput) (domain.EventTemplate, error) {
	f.gotUpdateTemplate = in
	f.gotUpdateTemplID = templateID
	return f.updateTemplateRet, f.updateTemplateErr
}

func (f *fakeCalendarService) DeleteEventTemplate(ctx context.Context, userID, templateID string) error {
	f.gotDeleteTemplID = templateID
	return f.deleteTemplateErr
}

func (f *fakeCalendarService) UseEventTemplate(ctx context.Context, userID, templateID string) error {
	f.useTemplateCalls++
	f.gotUseTemplID = templateID
	return f.useTemplateErr
}

// --- Calendar Set Methods

func (f *fakeCalendarService) ListCalendarSets(ctx context.Context, userID string) ([]domain.CalendarSet, error) {
	f.listSetsCalls++
	return f.listSetsRet, f.listSetsErr
}

func (f *fakeCalendarService) CreateCalendarSet(ctx context.Context, userID string, in domain.CalendarSetInput) (domain.CalendarSet, error) {
	f.gotCreateSet = in
	f.gotCreateSetUserID = userID
	return f.createSetRet, f.createSetErr
}

func (f *fakeCalendarService) UpdateCalendarSet(ctx context.Context, userID, setID string, in domain.CalendarSetInput) (domain.CalendarSet, error) {
	f.gotUpdateSet = in
	f.gotUpdateSetID = setID
	return f.updateSetRet, f.updateSetErr
}

func (f *fakeCalendarService) DeleteCalendarSet(ctx context.Context, userID, setID string) error {
	f.gotDeleteSetID = setID
	return f.deleteSetErr
}

// --- SearchService -----------------------------------------------------------

type fakeSearchService struct {
	ret   port.SearchResult
	err   error
	gotQ  string
	calls int
}

func (f *fakeSearchService) Search(ctx context.Context, userID, query string) (port.SearchResult, error) {
	f.calls++
	f.gotQ = query
	return f.ret, f.err
}

// --- AIService ---------------------------------------------------------------

type fakeAIService struct {
	ret    domain.AiComposeResponse
	err    error
	gotReq domain.AiComposeRequest

	askRet           domain.AiAskResponse
	askErr           error
	gotAskReq        domain.AiAskRequest
	proposeRet       domain.AiEventProposal
	proposeErr       error
	gotProposeThread string

	instantRepliesRet         []string
	instantRepliesErr         error
	gotInstantRepliesUserID   string
	gotInstantRepliesThreadID string

	listClassifiersRet []domain.AiClassifier
	listClassifiersErr error

	createClassifierRet    domain.AiClassifier
	createClassifierErr    error
	gotCreateClassifier    port.ClassifierInput
	gotCreateClassifierUID string

	updateClassifierRet   domain.AiClassifier
	updateClassifierErr   error
	gotUpdateClassifier   port.ClassifierInput
	gotUpdateClassifierID string

	deleteClassifierErr   error
	gotDeleteClassifierID string
}

func (f *fakeAIService) Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error) {
	f.gotReq = req
	return f.ret, f.err
}

func (f *fakeAIService) Ask(ctx context.Context, userID string, req domain.AiAskRequest) (domain.AiAskResponse, error) {
	f.gotAskReq = req
	return f.askRet, f.askErr
}

func (f *fakeAIService) ProposeEvent(ctx context.Context, userID, threadID string) (domain.AiEventProposal, error) {
	f.gotProposeThread = threadID
	return f.proposeRet, f.proposeErr
}

func (f *fakeAIService) InstantReplies(ctx context.Context, userID, threadID string) ([]string, error) {
	f.gotInstantRepliesUserID = userID
	f.gotInstantRepliesThreadID = threadID
	return f.instantRepliesRet, f.instantRepliesErr
}

func (f *fakeAIService) ListClassifiers(ctx context.Context, userID string) ([]domain.AiClassifier, error) {
	return f.listClassifiersRet, f.listClassifiersErr
}

func (f *fakeAIService) CreateClassifier(ctx context.Context, userID string, in port.ClassifierInput) (domain.AiClassifier, error) {
	f.gotCreateClassifier, f.gotCreateClassifierUID = in, userID
	return f.createClassifierRet, f.createClassifierErr
}

func (f *fakeAIService) UpdateClassifier(ctx context.Context, userID, classifierID string, in port.ClassifierInput) (domain.AiClassifier, error) {
	f.gotUpdateClassifier, f.gotUpdateClassifierID = in, classifierID
	return f.updateClassifierRet, f.updateClassifierErr
}

func (f *fakeAIService) DeleteClassifier(ctx context.Context, userID, classifierID string) error {
	f.gotDeleteClassifierID = classifierID
	return f.deleteClassifierErr
}

// --- DeviceService -----------------------------------------------------------

type fakeDeviceService struct {
	registerRet domain.NotificationDevice
	registerErr error
	gotPlatform domain.DevicePlatform
	gotToken    string

	unregisterErr error
	gotUnregID    string
}

func (f *fakeDeviceService) Register(ctx context.Context, userID string, platform domain.DevicePlatform, token string) (domain.NotificationDevice, error) {
	f.gotPlatform, f.gotToken = platform, token
	return f.registerRet, f.registerErr
}
func (f *fakeDeviceService) Unregister(ctx context.Context, userID, deviceID string) error {
	f.gotUnregID = deviceID
	return f.unregisterErr
}

// --- PrefsService -----------------------------------------------------------

type fakePrefsService struct {
	getRet domain.UserPrefs
	getErr error

	updateRet      domain.UserPrefs
	updateErr      error
	gotUpdatePrefs domain.UserPrefs
}

func (f *fakePrefsService) GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error) {
	return f.getRet, f.getErr
}
func (f *fakePrefsService) UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error) {
	f.gotUpdatePrefs = p
	return f.updateRet, f.updateErr
}

// --- SchedulingService --------------------------------------------------------
//
// fakeSchedulingService implements the full port.SchedulingService: the
// owner-authenticated methods (booking links, bookings, polls, proposals,
// guest free/busy, ExpireHolds) are real configurable doubles with
// captured-args fields; the public (unauthenticated) methods — PublicPage,
// PublicSlots, Book, PublicPollByToken, VotePoll — are covered by the
// parallel public-routes task's own fake and are simple zero-value stubs
// here so this type still satisfies the interface.

type fakeSchedulingService struct {
	// booking links
	createLinkRet       domain.BookingLink
	createLinkErr       error
	gotCreateLinkUserID string
	gotCreateLinkIn     port.BookingLinkInput

	updateLinkRet       domain.BookingLink
	updateLinkErr       error
	gotUpdateLinkUserID string
	gotUpdateLinkID     string
	gotUpdateLinkIn     port.BookingLinkInput

	listLinksRet       []domain.BookingLink
	listLinksErr       error
	gotListLinksUserID string

	deleteLinkErr       error
	gotDeleteLinkUserID string
	gotDeleteLinkID     string

	// bookings
	listBookingsRet []domain.Booking
	listBookingsErr error

	cancelBookingErr   error
	gotCancelBookingID string

	// public surface — stubs (owned by the public-routes task)
	publicPageRet  port.PublicBookingPage
	publicPageErr  error
	publicSlotsRet []domain.AvailabilitySlot
	publicSlotsErr error
	bookRet        domain.Booking
	bookErr        error

	// meeting polls
	createPollRet domain.MeetingPoll
	createPollErr error

	listPollsRet []domain.MeetingPoll
	listPollsErr error

	confirmPollRet         domain.MeetingPoll
	confirmPollErr         error
	gotConfirmPollID       string
	gotConfirmPollOptionID string

	deletePollErr   error
	gotDeletePollID string

	publicPollRet port.PublicPoll
	publicPollErr error
	votePollRet   port.PublicPoll
	votePollErr   error

	// propose-new-time
	proposeTimeRet domain.TimeProposal
	proposeTimeErr error

	listProposalsRet []domain.TimeProposal
	listProposalsErr error

	acceptProposalRet domain.Event
	acceptProposalErr error

	declineProposalErr error

	// guest free/busy
	freeBusyRet       map[string][]domain.BusyInterval
	freeBusyErr       error
	gotFreeBusyUserID string
	gotFreeBusyReq    port.FreeBusyRequest

	// Public-surface capture fields (public handler tests).
	gotPublicSlug string

	gotSlotsSlug string
	gotSlotsFrom time.Time
	gotSlotsTo   time.Time

	gotBookSlug string
	gotBookReq  port.BookingRequest

	gotPollToken string

	gotVoteToken  string
	gotVoteBallot port.PollBallot

	expireHoldsErr error
}

func (f *fakeSchedulingService) CreateLink(ctx context.Context, userID string, in port.BookingLinkInput) (domain.BookingLink, error) {
	f.gotCreateLinkUserID, f.gotCreateLinkIn = userID, in
	return f.createLinkRet, f.createLinkErr
}
func (f *fakeSchedulingService) UpdateLink(ctx context.Context, userID, linkID string, in port.BookingLinkInput) (domain.BookingLink, error) {
	f.gotUpdateLinkUserID, f.gotUpdateLinkID, f.gotUpdateLinkIn = userID, linkID, in
	return f.updateLinkRet, f.updateLinkErr
}
func (f *fakeSchedulingService) ListLinks(ctx context.Context, userID string) ([]domain.BookingLink, error) {
	f.gotListLinksUserID = userID
	return f.listLinksRet, f.listLinksErr
}
func (f *fakeSchedulingService) DeleteLink(ctx context.Context, userID, linkID string) error {
	f.gotDeleteLinkUserID, f.gotDeleteLinkID = userID, linkID
	return f.deleteLinkErr
}
func (f *fakeSchedulingService) ListBookings(ctx context.Context, userID string) ([]domain.Booking, error) {
	return f.listBookingsRet, f.listBookingsErr
}
func (f *fakeSchedulingService) CancelBooking(ctx context.Context, userID, bookingID string) error {
	f.gotCancelBookingID = bookingID
	return f.cancelBookingErr
}
func (f *fakeSchedulingService) PublicPage(ctx context.Context, slug string) (port.PublicBookingPage, error) {
	f.gotPublicSlug = slug
	return f.publicPageRet, f.publicPageErr
}
func (f *fakeSchedulingService) PublicSlots(ctx context.Context, slug string, from, to time.Time) ([]domain.AvailabilitySlot, error) {
	f.gotSlotsSlug, f.gotSlotsFrom, f.gotSlotsTo = slug, from, to
	return f.publicSlotsRet, f.publicSlotsErr
}
func (f *fakeSchedulingService) Book(ctx context.Context, slug string, req port.BookingRequest) (domain.Booking, error) {
	f.gotBookSlug, f.gotBookReq = slug, req
	return f.bookRet, f.bookErr
}
func (f *fakeSchedulingService) CreatePoll(ctx context.Context, userID string, in port.PollInput) (domain.MeetingPoll, error) {
	return f.createPollRet, f.createPollErr
}
func (f *fakeSchedulingService) ListPolls(ctx context.Context, userID string) ([]domain.MeetingPoll, error) {
	return f.listPollsRet, f.listPollsErr
}
func (f *fakeSchedulingService) ConfirmPoll(ctx context.Context, userID, pollID, optionID string) (domain.MeetingPoll, error) {
	f.gotConfirmPollID, f.gotConfirmPollOptionID = pollID, optionID
	return f.confirmPollRet, f.confirmPollErr
}
func (f *fakeSchedulingService) DeletePoll(ctx context.Context, userID, pollID string) error {
	f.gotDeletePollID = pollID
	return f.deletePollErr
}
func (f *fakeSchedulingService) PublicPollByToken(ctx context.Context, token string) (port.PublicPoll, error) {
	f.gotPollToken = token
	return f.publicPollRet, f.publicPollErr
}
func (f *fakeSchedulingService) VotePoll(ctx context.Context, token string, ballot port.PollBallot) (port.PublicPoll, error) {
	f.gotVoteToken, f.gotVoteBallot = token, ballot
	return f.votePollRet, f.votePollErr
}
func (f *fakeSchedulingService) ProposeTime(ctx context.Context, userID, eventID string, in port.TimeProposalInput) (domain.TimeProposal, error) {
	return f.proposeTimeRet, f.proposeTimeErr
}
func (f *fakeSchedulingService) ListProposals(ctx context.Context, userID, eventID string) ([]domain.TimeProposal, error) {
	return f.listProposalsRet, f.listProposalsErr
}
func (f *fakeSchedulingService) AcceptProposal(ctx context.Context, userID, eventID, proposalID string) (domain.Event, error) {
	return f.acceptProposalRet, f.acceptProposalErr
}
func (f *fakeSchedulingService) DeclineProposal(ctx context.Context, userID, eventID, proposalID string) error {
	return f.declineProposalErr
}
func (f *fakeSchedulingService) GuestFreeBusy(ctx context.Context, userID string, req port.FreeBusyRequest) (map[string][]domain.BusyInterval, error) {
	f.gotFreeBusyUserID, f.gotFreeBusyReq = userID, req
	return f.freeBusyRet, f.freeBusyErr
}
func (f *fakeSchedulingService) ExpireHolds(ctx context.Context) error {
	return f.expireHoldsErr
}

// --- SettingsService -----------------------------------------------------------

type fakeSettingsService struct {
	getRet domain.UserSettings
	getErr error

	updateRet       domain.UserSettings
	updateErr       error
	gotUpdateUserID string
	gotUpdateIn     domain.UserSettings
}

func (f *fakeSettingsService) Get(ctx context.Context, userID string) (domain.UserSettings, error) {
	return f.getRet, f.getErr
}
func (f *fakeSettingsService) Update(ctx context.Context, userID string, s domain.UserSettings) (domain.UserSettings, error) {
	f.gotUpdateUserID, f.gotUpdateIn = userID, s
	return f.updateRet, f.updateErr
}

// --- harness -----------------------------------------------------------------

type harness struct {
	t          *testing.T
	deps       Deps
	verifier   *fakeVerifier
	users      *fakeUserService
	billing    *fakeBillingService
	accounts   *fakeAccountService
	mail       *fakeMailService
	calendars  *fakeCalendarService
	search     *fakeSearchService
	ai         *fakeAIService
	devices    *fakeDeviceService
	prefs      *fakePrefsService
	sched      *fakeSchedulingService
	scheduling *fakeSchedulingService // alias of sched (public-surface tests)
	settings   *fakeSettingsService
}

// newHarness wires every double into Deps with a discard logger and one
// pre-registered valid token (defaultToken => user defaultUserID).
func newHarness(t *testing.T) *harness {
	t.Helper()
	ver := &fakeVerifier{tokens: map[string]port.Identity{
		defaultToken: {Subject: defaultUserID, Email: "owner@example.com", Name: "Owner"},
	}}
	users := &fakeUserService{ensureRet: domain.User{ID: defaultUserID, Email: "owner@example.com"}}
	h := &harness{
		t:         t,
		verifier:  ver,
		users:     users,
		billing:   &fakeBillingService{},
		accounts:  &fakeAccountService{},
		mail:      &fakeMailService{},
		calendars: &fakeCalendarService{},
		search:    &fakeSearchService{},
		ai:        &fakeAIService{},
		devices:   &fakeDeviceService{},
		prefs:     &fakePrefsService{},
		sched:     &fakeSchedulingService{},
		settings:  &fakeSettingsService{},
	}
	h.scheduling = h.sched
	h.deps = Deps{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Verifier:   ver,
		Users:      users,
		Billing:    h.billing,
		Accounts:   h.accounts,
		Mail:       h.mail,
		Calendars:  h.calendars,
		Search:     h.search,
		AI:         h.ai,
		Devices:    h.devices,
		Prefs:      h.prefs,
		Scheduling: h.sched,
		Settings:   h.settings,
	}
	return h
}

// server returns a bare *server for unit-testing individual middleware
// (requireAuth/recoverPanics) without the full New() stack.
func (h *harness) server() *server { return &server{deps: h.deps} }

// handler returns the full v1 stack (recover + log + CORS + auth + routes).
func (h *harness) handler() http.Handler { return New(h.deps) }

// authed issues a request through the full stack with a valid bearer token.
func (h *harness) authed(method, target string, body io.Reader) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

// anon issues a request through the full stack with no Authorization header.
func (h *harness) anon(method, target string, body io.Reader) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

// jsonBody marshals v to an io.Reader for request bodies.
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(b)
}

// decodeErr unmarshals the { "error": { code, message } } envelope.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	return b.Error
}
