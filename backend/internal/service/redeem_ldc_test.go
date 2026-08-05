package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func TestCalculateLDCCodeCreditUSDUsesTieredUserQuota(t *testing.T) {
	tests := []struct {
		name          string
		ldcAmount     float64
		issuedUSD     float64
		wantCreditUSD float64
	}{
		{name: "all promo", ldcAmount: 100, issuedUSD: 0, wantCreditUSD: 10},
		{name: "partial promo then normal", ldcAmount: 100, issuedUSD: 6, wantCreditUSD: 5.2},
		{name: "promo exhausted", ldcAmount: 100, issuedUSD: 10, wantCreditUSD: 2},
		{name: "more than promo from zero", ldcAmount: 150, issuedUSD: 0, wantCreditUSD: 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.InDelta(t, tt.wantCreditUSD, calculateLDCCodeCreditUSD(tt.ldcAmount, tt.issuedUSD), 0.000001)
		})
	}
}

func TestRedeemCodeLDCMetadataDetection(t *testing.T) {
	meta, ok := parseLDCCodeMetadata(`{"code_kind":"ldc","source":"manual_ldc_code"}`)
	require.True(t, ok)
	require.Equal(t, "ldc", meta.CodeKind)

	_, ok = parseLDCCodeMetadata(`{"source":"linuxdo_credit_shop","usd_value":3}`)
	require.False(t, ok)
}

func TestMergeLDCCodeRedemptionMetadataKeepsOriginalAndRecordsCredit(t *testing.T) {
	got := mergeLDCCodeRedemptionNotes(`{"code_kind":"ldc","batch":"a"}`, 39, 100, 10)

	require.Contains(t, got, `"code_kind":"ldc"`)
	require.Contains(t, got, `"batch":"a"`)
	require.Contains(t, got, `"redeemed_user_id":39`)
	require.Contains(t, got, `"ldc_amount":100`)
	require.Contains(t, got, `"credited_usd":10`)
}

func TestRedeemLDCCodeCreditsTieredUSDByUserHistoryWithLinuxDoBinding(t *testing.T) {
	ctx := context.Background()
	client := newLDCRedeemTestClient(t)

	user, err := client.User.Create().
		SetEmail("ldc-redeem@example.com").
		SetPasswordHash("hash").
		SetUsername("ldc-user").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("subject-123").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RedeemCode.Create().
		SetCode("OLD-LDC-USD").
		SetType(RedeemTypeBalance).
		SetValue(100).
		SetStatus(StatusUsed).
		SetUsedBy(user.ID).
		SetUsedAt(time.Now().UTC().Add(-time.Hour)).
		SetNotes(`{"code_kind":"ldc","redeemed_user_id":39,"ldc_amount":60,"credited_usd":6}`).
		Save(ctx)
	require.NoError(t, err)

	code, err := client.RedeemCode.Create().
		SetCode("LDC-CODE-100").
		SetType(RedeemTypeBalance).
		SetValue(100).
		SetStatus(StatusUnused).
		SetNotes(`{"code_kind":"ldc","batch":"manual-a"}`).
		Save(ctx)
	require.NoError(t, err)

	userRepo := &ldcUserRepoStub{user: &User{ID: user.ID, Balance: 0}}
	svc := NewRedeemService(&ldcRedeemRepoStub{client: client}, userRepo, nil, nil, nil, client, nil, nil)

	got, err := svc.Redeem(ctx, user.ID, code.Code)
	require.NoError(t, err)
	require.InDelta(t, 5.2, userRepo.lastBalanceAmount, 0.000001)
	require.InDelta(t, 5.2, got.Value, 0.000001)

	reloaded, err := client.RedeemCode.Get(ctx, code.ID)
	require.NoError(t, err)
	require.Equal(t, StatusUsed, reloaded.Status)
	require.InDelta(t, 100, reloaded.Value, 0.000001)
	require.NotNil(t, reloaded.Notes)
	require.Contains(t, *reloaded.Notes, `"code_kind":"ldc"`)
	require.Contains(t, *reloaded.Notes, `"redeemed_user_id":`)
	require.Contains(t, *reloaded.Notes, `"ldc_amount":100`)
	require.Contains(t, *reloaded.Notes, `"credited_usd":5.2`)
}

func TestRedeemLDCCodeRequiresLinuxDoBinding(t *testing.T) {
	ctx := context.Background()
	client := newLDCRedeemTestClient(t)

	user, err := client.User.Create().
		SetEmail("ldc-no-bind@example.com").
		SetPasswordHash("hash").
		SetUsername("ldc-no-bind-user").
		Save(ctx)
	require.NoError(t, err)

	code, err := client.RedeemCode.Create().
		SetCode("LDC-NO-BIND").
		SetType(RedeemTypeBalance).
		SetValue(100).
		SetStatus(StatusUnused).
		SetNotes(`{"code_kind":"ldc"}`).
		Save(ctx)
	require.NoError(t, err)

	userRepo := &ldcUserRepoStub{user: &User{ID: user.ID, Balance: 0}}
	svc := NewRedeemService(&ldcRedeemRepoStub{client: client}, userRepo, nil, nil, nil, client, nil, nil)

	got, err := svc.Redeem(ctx, user.ID, code.Code)
	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, "LDC_REDEEM_LINUXDO_BIND_REQUIRED", infraerrors.Reason(err))
	require.Zero(t, userRepo.updateBalanceCalls)

	reloaded, err := client.RedeemCode.Get(ctx, code.ID)
	require.NoError(t, err)
	require.Equal(t, StatusUnused, reloaded.Status)
	require.Nil(t, reloaded.UsedBy)
}

func TestRedeemLDCCodeRequiresMatchingLinuxDoSubjectWhenCodeIsUserScoped(t *testing.T) {
	ctx := context.Background()
	client := newLDCRedeemTestClient(t)

	user, err := client.User.Create().
		SetEmail("ldc-mismatch@example.com").
		SetPasswordHash("hash").
		SetUsername("ldc-mismatch-user").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("other-subject").
		Save(ctx)
	require.NoError(t, err)

	code, err := client.RedeemCode.Create().
		SetCode("LDC-SUBJECT-SCOPED").
		SetType(RedeemTypeBalance).
		SetValue(100).
		SetStatus(StatusUnused).
		SetNotes(`{"code_kind":"ldc","linuxdo_user_sub":"expected-subject"}`).
		Save(ctx)
	require.NoError(t, err)

	userRepo := &ldcUserRepoStub{user: &User{ID: user.ID, Balance: 0}}
	svc := NewRedeemService(&ldcRedeemRepoStub{client: client}, userRepo, nil, nil, nil, client, nil, nil)

	got, err := svc.Redeem(ctx, user.ID, code.Code)
	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, "LDC_REDEEM_LINUXDO_SUBJECT_MISMATCH", infraerrors.Reason(err))
	require.Zero(t, userRepo.updateBalanceCalls)

	reloaded, err := client.RedeemCode.Get(ctx, code.ID)
	require.NoError(t, err)
	require.Equal(t, StatusUnused, reloaded.Status)
	require.Nil(t, reloaded.UsedBy)
}

func TestRedeemLDCCodeAllowsMatchingLinuxDoSubject(t *testing.T) {
	ctx := context.Background()
	client := newLDCRedeemTestClient(t)

	user, err := client.User.Create().
		SetEmail("ldc-match@example.com").
		SetPasswordHash("hash").
		SetUsername("ldc-match-user").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("expected-subject").
		Save(ctx)
	require.NoError(t, err)

	code, err := client.RedeemCode.Create().
		SetCode("LDC-SUBJECT-MATCH").
		SetType(RedeemTypeBalance).
		SetValue(100).
		SetStatus(StatusUnused).
		SetNotes(`{"code_kind":"ldc","linuxdo_user_sub":"expected-subject"}`).
		Save(ctx)
	require.NoError(t, err)

	userRepo := &ldcUserRepoStub{user: &User{ID: user.ID, Balance: 0}}
	svc := NewRedeemService(&ldcRedeemRepoStub{client: client}, userRepo, nil, nil, nil, client, nil, nil)

	got, err := svc.Redeem(ctx, user.ID, code.Code)
	require.NoError(t, err)
	require.InDelta(t, 10, userRepo.lastBalanceAmount, 0.000001)
	require.InDelta(t, 10, got.Value, 0.000001)
}

type ldcRedeemRepoStub struct {
	client *dbent.Client
}

func (r *ldcRedeemRepoStub) Create(context.Context, *RedeemCode) error { panic("unexpected call") }
func (r *ldcRedeemRepoStub) CreateBatch(context.Context, []RedeemCode) error {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	m, err := r.client.RedeemCode.Get(ctx, id)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return ldcRedeemCodeFromEnt(m), nil
}
func (r *ldcRedeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	m, err := r.client.RedeemCode.Query().Where(redeemcode.CodeEQ(code)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return ldcRedeemCodeFromEnt(m), nil
}
func (r *ldcRedeemRepoStub) Update(context.Context, *RedeemCode) error { panic("unexpected call") }
func (r *ldcRedeemRepoStub) BatchUpdate(context.Context, []int64, RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) Delete(context.Context, int64) error { panic("unexpected call") }
func (r *ldcRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	client := r.client
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}
	affected, err := client.RedeemCode.Update().
		Where(redeemcode.IDEQ(id), redeemcode.StatusEQ(StatusUnused)).
		SetStatus(StatusUsed).
		SetUsedBy(userID).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrRedeemCodeUsed
	}
	return nil
}
func (r *ldcRedeemRepoStub) List(context.Context, pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) ListByUser(context.Context, int64, int) ([]RedeemCode, error) {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}
func (r *ldcRedeemRepoStub) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected call")
}

func ldcRedeemCodeFromEnt(m *dbent.RedeemCode) *RedeemCode {
	if m == nil {
		return nil
	}
	notes := ""
	if m.Notes != nil {
		notes = *m.Notes
	}
	return &RedeemCode{
		ID:           m.ID,
		Code:         m.Code,
		Type:         m.Type,
		Value:        m.Value,
		Status:       m.Status,
		UsedBy:       m.UsedBy,
		UsedAt:       m.UsedAt,
		Notes:        notes,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
		GroupID:      m.GroupID,
		ValidityDays: m.ValidityDays,
	}
}

type ldcUserRepoStub struct {
	user               *User
	lastBalanceAmount  float64
	updateBalanceCalls int
}

func (r *ldcUserRepoStub) Create(context.Context, *User) error { panic("unexpected call") }
func (r *ldcUserRepoStub) CreateWithEmailAliasGuard(context.Context, *User) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	if r.user == nil {
		return &User{}, nil
	}
	cloned := *r.user
	return &cloned, nil
}
func (r *ldcUserRepoStub) GetByIDIncludeDeleted(context.Context, int64) (*User, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) GetByEmail(context.Context, string) (*User, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) GetFirstAdmin(context.Context) (*User, error) { panic("unexpected call") }
func (r *ldcUserRepoStub) Update(context.Context, *User, UserUpdateFields) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) Delete(context.Context, int64) error { panic("unexpected call") }
func (r *ldcUserRepoStub) GetUserAvatar(context.Context, int64) (*UserAvatar, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UpsertUserAvatar(context.Context, int64, UpsertUserAvatarInput) (*UserAvatar, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) DeleteUserAvatar(context.Context, int64) error { panic("unexpected call") }
func (r *ldcUserRepoStub) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UpdateBalance(_ context.Context, _ int64, amount float64) error {
	r.lastBalanceAmount = amount
	r.updateBalanceCalls++
	if r.user != nil {
		r.user.Balance += amount
	}
	return nil
}
func (r *ldcUserRepoStub) DeductBalance(context.Context, int64, float64) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) AdjustBalance(context.Context, int64, float64) (BalanceChange, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) SetBalance(context.Context, int64, float64) (BalanceChange, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) ExistsByEmail(context.Context, string) (bool, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) ExistsByEmailAlias(context.Context, string) (bool, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) ListUserAuthIdentities(context.Context, int64) ([]UserAuthIdentityRecord, error) {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected call")
}
func (r *ldcUserRepoStub) EnableTotp(context.Context, int64) error  { panic("unexpected call") }
func (r *ldcUserRepoStub) DisableTotp(context.Context, int64) error { panic("unexpected call") }

func newLDCRedeemTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	db, err := sql.Open("sqlite", "file:ldc_redeem?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}
