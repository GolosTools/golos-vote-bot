package models

import (
	"database/sql"
	"errors"
)

type Credential struct {
	UserID   int
	UserName string
	Rating   int
	Active   bool
}

func (credential Credential) Save(db *sql.DB) (bool, error) {
	prepare, err := db.Prepare("INSERT OR REPLACE INTO credentials(" +
		"user_id," +
		"user_name," +
		"rating," +
		"active) " +
		"values(?, ?, ?, ?)")
	defer prepare.Close()
	if err != nil {
		return false, err
	}
	_, err = prepare.Exec(
		credential.UserID,
		credential.UserName,
		credential.Rating,
		credential.Active)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (credential Credential) Exists(db *sql.DB) bool {
	row := db.QueryRow("SELECT user_id FROM credentials WHERE user_id = ? AND user_name = ?",
		credential.UserID, credential.UserName)
	var userID *int
	row.Scan(&userID)
	if userID != nil {
		return true
	}
	return false
}

func (credential Credential) IncrementRating(db *sql.DB, rating int) error {
	_, err := db.Exec("UPDATE credentials SET rating = rating + ? WHERE user_id = ?",
		rating, credential.UserID)
	return err
}

func (credential Credential) DecrementRating(db *sql.DB, rating int) error {
	_, err := db.Exec("UPDATE credentials SET rating = rating - ? WHERE user_id = ?",
		rating, credential.UserID)
	return err
}

func (credential Credential) GetRating(db *sql.DB) (int, error) {
	row := db.QueryRow("SELECT rating FROM credentials WHERE user_id = ?",
		credential.UserID)
	var rating *int
	row.Scan(&rating)
	if rating != nil {
		return *rating, nil
	}
	return 0, errors.New("не получили рейтинг из базы данных")
}

func GetCredentialByUserID(userID int, db *sql.DB) (credential Credential, err error) {
	row := db.QueryRow("SELECT user_id, user_name, rating, active FROM credentials WHERE user_id = ?", userID)
	err = row.Scan(&credential.UserID, &credential.UserName, &credential.Rating, &credential.Active)
	return credential, err
}

func GetAllCredentials(db *sql.DB) (credentials []Credential, err error) {
	rows, err := db.Query("SELECT user_id, user_name, rating, active FROM credentials")
	if err != nil {
		return credentials, err
	}
	defer rows.Close()
	for rows.Next() {
		var credential Credential
		err := rows.Scan(&credential.UserID, &credential.UserName, &credential.Rating, &credential.Active)
		if err == nil && credential.Active {
			credentials = append(credentials, credential)
		}
	}
	return credentials, err
}
