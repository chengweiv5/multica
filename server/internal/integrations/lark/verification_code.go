package lark

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type verificationCodeDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// VerificationCodeNotifier sends a login code to every active Feishu/Lark
// installation the user has explicitly bound.
type VerificationCodeNotifier struct {
	db            verificationCodeDB
	installations *InstallationService
	client        APIClient
}

func NewVerificationCodeNotifier(db verificationCodeDB, installations *InstallationService, client APIClient) *VerificationCodeNotifier {
	return &VerificationCodeNotifier{db: db, installations: installations, client: client}
}

func (n *VerificationCodeNotifier) SendVerificationCode(ctx context.Context, userID pgtype.UUID, code string) error {
	rows, err := n.db.Query(ctx, `
		SELECT ci.config, cub.channel_user_id
		FROM channel_user_binding cub
		JOIN channel_installation ci ON ci.id = cub.installation_id
		WHERE cub.multica_user_id = $1
		  AND cub.channel_type = 'feishu'
		  AND ci.channel_type = 'feishu'
		  AND ci.status = 'active'
		ORDER BY cub.bound_at DESC
	`, userID)
	if err != nil {
		return fmt.Errorf("list bound Lark accounts: %w", err)
	}
	defer rows.Close()

	var sendErrs []error
	for rows.Next() {
		var config []byte
		var openID string
		if err := rows.Scan(&config, &openID); err != nil {
			return fmt.Errorf("scan bound Lark account: %w", err)
		}
		inst, err := installationFromRow(db.ChannelInstallation{Config: config})
		if err != nil {
			sendErrs = append(sendErrs, err)
			continue
		}
		secret, err := n.installations.DecryptAppSecret(inst)
		if err != nil {
			sendErrs = append(sendErrs, err)
			continue
		}
		_, err = n.client.SendTextMessage(ctx, SendTextParams{
			InstallationID: InstallationCredentials{
				AppID: inst.AppID, AppSecret: secret, TenantKey: inst.TenantKey.String, Region: RegionOrDefault(inst.Region),
			},
			ChatID:        ChatID(openID),
			ReceiveIDType: "open_id",
			Text:          "Your Multica login verification code is: " + code + "\nIt expires in 10 minutes.",
		})
		if err != nil {
			sendErrs = append(sendErrs, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list bound Lark accounts: %w", err)
	}
	return errors.Join(sendErrs...)
}
