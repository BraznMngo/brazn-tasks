// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-present Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package user

import (
	"regexp"
	"strings"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/modules/brazn/locale"
	"code.vikunja.io/api/pkg/notifications"
	"golang.org/x/crypto/bcrypt"
	"xorm.io/xorm"
)

const (
	IssuerLocal = `local`
	IssuerLDAP  = `ldap`
)

// CreateUser creates a new user and inserts it into the database
func CreateUser(s *xorm.Session, user *User) (*User, error) {
	newUser, confirm, err := CreateUserConfirmLater(s, user)
	if err != nil {
		return newUser, err
	}

	return newUser, SendConfirmation(confirm, s)
}

// CreateUserConfirmLater is CreateUser with the confirmation mail left unsent
// and handed back instead. Everything else is identical, and in particular the
// confirmation token and the StatusEmailConfirmationRequired status are still
// written inside s, so they roll back with the rest of the registration.
//
// It exists because sending that mail is the one step of a registration that
// CANNOT BE UNDONE. A caller that may still refuse after the user row exists -
// managed mode redeems the signup token against the new users.id, so it can
// only ask once there is an id to ask about (BRA-1111) - would otherwise have
// mailed an address the caller chose before deciding whether to keep the
// account. The row comes back on rollback; the mail does not.
//
// The returned notification is nil when no confirmation is due (no mailer, or a
// user this instance did not issue credentials for). Send it with
// SendConfirmation, and ONLY once s has committed.
func CreateUserConfirmLater(s *xorm.Session, user *User) (newUser *User, confirm *EmailConfirmNotification, err error) {

	if user.Issuer == "" {
		user.Issuer = IssuerLocal
	}

	// Check if we have all required information
	err = checkIfUserIsValid(user)
	if err != nil {
		return nil, nil, err
	}

	// Check if the user already exists with that username
	err = checkIfUserExists(s, user)
	if err != nil {
		return nil, nil, err
	}

	if user.Issuer == IssuerLocal {
		// Hash the password
		user.Password, err = HashPassword(user.Password)
		if err != nil {
			return nil, nil, err
		}
	}

	user.ID = 0
	user.Status = StatusActive
	user.AvatarProvider = config.DefaultSettingsAvatarProvider.GetString()
	user.AvatarFileID = config.DefaultSettingsAvatarFileID.GetInt64()
	user.EmailRemindersEnabled = config.DefaultSettingsEmailRemindersEnabled.GetBool()
	user.DiscoverableByName = config.DefaultSettingsDiscoverableByName.GetBool()
	user.DiscoverableByEmail = config.DefaultSettingsDiscoverableByEmail.GetBool()
	user.OverdueTasksRemindersEnabled = config.DefaultSettingsOverdueTaskRemindersEnabled.GetBool()
	user.OverdueTasksRemindersTime = config.DefaultSettingsOverdueTaskRemindersTime.GetString()
	user.DefaultProjectID = config.DefaultSettingsDefaultProjectID.GetInt64()
	user.WeekStart = config.DefaultSettingsWeekStart.GetInt()
	user.Timezone = config.DefaultSettingsTimezone.GetString()

	if user.Language == "" {
		user.Language = config.DefaultSettingsLanguage.GetString()
	}

	// BRA-860: store the catalogue code, so the frontend and the notification
	// mailer resolve the same language from the same column.
	user.Language = locale.Normalize(user.Language)

	// Insert it
	_, err = s.Insert(user)
	if err != nil {
		return nil, nil, err
	}

	// Get the  full new User
	newUserOut, err := GetUserByID(s, user.ID)
	if err != nil && !IsErrUserStatusError(err) {
		return nil, nil, err
	}

	events.DispatchOnCommit(s, &CreatedEvent{
		User: newUserOut,
	})

	// Don't send a mail if no mailer is configured
	if !config.MailerEnabled.GetBool() || user.Issuer != IssuerLocal {
		return newUserOut, nil, err
	}

	user.Status = StatusEmailConfirmationRequired
	token, err := generateToken(s, user, TokenEmailConfirm)
	if err != nil {
		return nil, nil, err
	}

	_, err = s.
		Where("id = ?", user.ID).
		Cols("email", "status").
		Update(user)
	if err != nil {
		return
	}

	return newUserOut, &EmailConfirmNotification{
		User:         user,
		IsNew:        true,
		ConfirmToken: token.ClearTextToken,
	}, nil
}

// SendConfirmation sends the confirmation mail CreateUserConfirmLater left
// unsent. A nil notification is a no-op, so a caller hands back whatever it got
// without having to ask whether a confirmation was due.
//
// Pass the session only while the transaction that created the user is still
// open. A caller that deferred the mail PAST its commit must pass none, because
// that session is finished; the notifiable is then read on a fresh one.
func SendConfirmation(confirm *EmailConfirmNotification, sessions ...*xorm.Session) error {
	if confirm == nil {
		return nil
	}

	return notifications.Notify(confirm.User, confirm, sessions...)
}

// CreateBotUser creates a bot user owned by the given owner.
// Bots have no email or password and cannot authenticate interactively.
// It intentionally bypasses checkIfUserIsValid / checkIfUserExists because
// those enforce email+password and would flag duplicate empty emails.
func CreateBotUser(s *xorm.Session, bot *User, owner *User) (*User, error) {
	if owner == nil || owner.ID == 0 {
		return nil, ErrNoUsernamePassword{}
	}
	if owner.IsBot() {
		return nil, &ErrBotNotOwned{UserID: owner.ID}
	}

	// Reuse the same username format rules as regular user creation
	if err := checkUsernameFormat(bot.Username); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(bot.Username, "bot-") {
		return nil, &ErrBotUsernameMustHavePrefix{Username: bot.Username}
	}

	if _, err := GetUserByUsername(s, bot.Username); err == nil {
		return nil, ErrUsernameExists{Username: bot.Username}
	} else if !IsErrUserDoesNotExist(err) {
		return nil, err
	}

	bot.ID = 0
	bot.BotOwnerID = owner.ID
	bot.Status = StatusActive
	bot.Issuer = IssuerLocal
	bot.Password = ""
	bot.Email = ""

	if _, err := s.Insert(bot); err != nil {
		return nil, err
	}

	newBot, err := GetUserByID(s, bot.ID)
	if err != nil {
		return nil, err
	}

	events.DispatchOnCommit(s, &CreatedEvent{User: newBot})
	return newBot, nil
}

// HashPassword hashes a password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), config.ServiceBcryptRounds.GetInt())
	return string(bytes), err
}

// checkUsernameFormat validates username format rules shared by regular and bot users.
func checkUsernameFormat(username string) error {
	if username == "" {
		return ErrNoUsernamePassword{}
	}

	if strings.Contains(username, " ") {
		return &ErrUsernameMustNotContainSpaces{
			Username: username,
		}
	}

	// Check if username matches the reserved link-share pattern
	linkSharePattern := regexp.MustCompile(`^link-share-\d+$`)
	if linkSharePattern.MatchString(username) {
		return ErrUsernameReserved{
			Username: username,
		}
	}

	return nil
}

func checkIfUserIsValid(user *User) error {
	if user.Email == "" ||
		(user.Issuer != IssuerLocal && user.Subject == "") ||
		(user.Issuer == IssuerLocal && (user.Password == "" ||
			user.Username == "")) {
		return ErrNoUsernamePassword{}
	}

	if err := checkUsernameFormat(user.Username); err != nil {
		return err
	}

	// Reserve the bot- prefix for bot users (created via CreateBotUser)
	if strings.HasPrefix(user.Username, "bot-") {
		return ErrUsernameReserved{
			Username: user.Username,
		}
	}

	return nil
}

func checkIfUserExists(s *xorm.Session, user *User) (err error) {
	exists := true
	_, err = GetUserByUsername(s, user.Username)
	if err != nil {
		if IsErrUserDoesNotExist(err) {
			exists = false
		} else {
			return err
		}
	}
	if exists {
		return ErrUsernameExists{user.ID, user.Username}
	}

	// Check if the user already existst with that email
	exists = true
	userToCheck := &User{
		Email:   user.Email,
		Issuer:  user.Issuer,
		Subject: user.Subject,
	}

	if user.Issuer != IssuerLocal {
		userToCheck.Email = ""
	}

	_, err = getUser(s, userToCheck, false)
	if err != nil {
		if IsErrUserDoesNotExist(err) {
			exists = false
		} else {
			return err
		}
	}
	if exists && user.Issuer == IssuerLocal {
		return ErrUserEmailExists{user.ID, user.Email}
	}

	return nil
}
