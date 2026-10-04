package provider

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// Only the operator-owned writer reads raw credentials. The returned bootstrap
// contains their digests; replicas receive the signed sys revision instead.
func loadSecurityMachines(path string) ([]security.BootstrapMachine, error) {
	raw, err := readSecurityOperatorFile(path, true, 1<<20)
	if err != nil {
		return nil, errors.New("machine bootstrap file unavailable or unsafe")
	}
	defer clear(raw)
	var machines []struct {
		Name        string   `json:"name"`
		RoleIDs     []string `json:"role_ids"`
		Credentials []struct {
			Token     string    `json:"token"`
			CreatedAt time.Time `json:"created_at"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"credentials"`
	}
	if err := strictSecurityConfigJSON(string(raw), &machines); err != nil || len(machines) == 0 || len(machines) > 64 {
		return nil, errors.New("invalid machine bootstrap configuration")
	}
	result := make([]security.BootstrapMachine, len(machines))
	for i, machine := range machines {
		if len(machine.Credentials) == 0 || len(machine.Credentials) > 4 {
			return nil, errors.New("invalid machine credential count")
		}
		result[i] = security.BootstrapMachine{Name: machine.Name, RoleIDs: machine.RoleIDs}
		for _, credential := range machine.Credentials {
			digest, valid := security.MachineTokenDigest(credential.Token)
			if !valid {
				return nil, errors.New("invalid machine credential format")
			}
			result[i].Credentials = append(result[i].Credentials, security.MachineCredential{
				Identity: security.Identity{Kind: security.MachinePrincipal, MachineName: machine.Name},
				Digest:   digest, CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt,
			})
		}
	}
	return result, nil
}
