package vault

import (
	"errors"
	"fmt"
	s "github.com/gherynos/vault-backend/store"
	"github.com/hashicorp/vault/api"
	log "github.com/sirupsen/logrus"
	"sync"
	"time"
)

// Vault is a client to communicate with an instance of Hashicorp's Vault.
type Vault struct {
	roleID, secretID, prefix string
	client                   *api.Client

	tokenExpiration time.Time

	m sync.Mutex
}

// NewWithToken creates a new Vault client using an authentication token.
// VaultURL is the URL of the Vault server to connect to.
// prefix is the string prefix used when storing the secrets in Vault.
func NewWithToken(vaultURL, token, prefix string) (out *Vault, err error) {

	var v Vault
	if v.client, err = api.NewClient(&api.Config{Address: vaultURL}); err != nil {

		return nil, err
	}

	v.prefix = prefix
	v.client.SetToken(token)

	return &v, nil
}

// NewWithAppRole creates a new Vault client using AppRole as the authentication method.
// The token retrieved using roleID and secretID is automatically refreshed.
// VaultURL is the URL of the Vault server to connect to.
// prefix is the string prefix used when storing the secrets in Vault.
func NewWithAppRole(vaultURL, roleID, secretID, prefix string) (out *Vault, err error) {

	var v Vault
	if v.client, err = api.NewClient(&api.Config{Address: vaultURL}); err != nil {

		return nil, err
	}

	v.roleID = roleID
	v.secretID = secretID
	v.prefix = prefix

	if err = v.authenticate(); err != nil {

		return nil, err
	}

	return &v, nil
}

func (v *Vault) authenticate() (err error) {

	options := map[string]interface{}{
		"role_id":   v.roleID,
		"secret_id": v.secretID,
	}

	var secret *api.Secret
	if secret, err = v.client.Logical().Write("auth/approle/login", options); err != nil {

		return err
	}

	v.client.SetToken(secret.Auth.ClientToken)
	v.tokenExpiration = time.Now()
	if secret.Auth.Renewable {

		v.tokenExpiration = v.tokenExpiration.Add(time.Duration(secret.Auth.LeaseDuration-60) * time.Second)
	}

	return nil
}

func (v *Vault) refreshToken() error {

	// only refresh the token when using AppRole
	if v.roleID == "" && v.secretID == "" {

		return nil
	}

	// re-authenticate if the token has expired
	v.m.Lock()
	if v.tokenExpiration.Before(time.Now()) {

		log.Debug("Refreshing Vault token...")

		if err := v.authenticate(); err != nil {

			v.m.Unlock()
			return err
		}
	}
	v.m.Unlock()

	return nil
}

// Set populates a Vault secret content.
func (v *Vault) Set(name, data string) error {

	if err := v.refreshToken(); err != nil {

		return err
	}

	if _, err := v.client.Logical().Write(fmt.Sprintf("secret/data/%s/%s", v.prefix, name),
		map[string]interface{}{"data": map[string]interface{}{"value": data}}); err != nil {

		return err
	}

	return nil
}

// SetBin populates a Vault secret content using binary data.
func (v *Vault) SetBin(name string, data []byte) (err error) {

	var value string
	if value, err = Encode(data); err != nil {

		return
	}

	return v.Set(name, value)
}

// Get retrieves the content of a Vault secret.
func (v *Vault) Get(name string) (out string, err error) {

	if err = v.refreshToken(); err != nil {

		return
	}

	var secret *api.Secret
	if secret, err = v.client.Logical().Read(fmt.Sprintf("secret/data/%s/%s", v.prefix, name)); err != nil {

		return
	}

	if secret == nil {

		return "", &s.ItemNotFoundError{}
	}

	if data, ok := secret.Data["data"].(map[string]interface{}); ok {

		return data["value"].(string), nil
	}

	return "", errors.New("unable to convert secret data")
}

// GetBin retrieves the binary content of a Vault secret.
func (v *Vault) GetBin(name string) (out []byte, err error) {

	var value string
	if value, err = v.Get(name); err != nil {

		return
	}

	return Decode(value)
}

// Delete removes a secret from Vault.
func (v *Vault) Delete(name string) error {

	if err := v.refreshToken(); err != nil {

		return err
	}

	if _, err := v.client.Logical().Delete(fmt.Sprintf("secret/metadata/%s/%s", v.prefix, name)); err != nil {

		return err
	}

	return nil
}
