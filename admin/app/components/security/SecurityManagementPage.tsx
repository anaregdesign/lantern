import { Title2 } from "@fluentui/react-components";
import { Link } from "react-router";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { useSecurityManagementClient } from "~/lib/client/infrastructure/api/use-security-management-client";
import type { SecuritySection } from "~/lib/client/usecase/security/security-management";
import { SecurityManagementView } from "./SecurityManagementView";
import styles from "./SecurityManagementPage.module.css";

export function SecurityManagementPage({
  section,
}: {
  section: SecuritySection;
}) {
  const auth = useAuth();
  const port = useSecurityManagementClient();
  return (
    <section className={styles.page}>
      <Title2>Security</Title2>
      <nav className={styles.nav} aria-label="Security">
        <Link
          to="/security/issuers"
          aria-current={section === "issuers" ? "page" : undefined}
        >
          Issuers
        </Link>
        <Link
          to="/security/users"
          aria-current={section === "users" ? "page" : undefined}
        >
          Users
        </Link>
        <Link
          to="/security/roles"
          aria-current={section === "roles" ? "page" : undefined}
        >
          Roles
        </Link>
      </nav>
      {auth.state.kind === "off" ? (
        <p>
          Authentication is off. The operator must configure OIDC and the
          environment-owned administrator before security management is
          available.
        </p>
      ) : auth.state.kind === "ready" && port ? (
        <SecurityManagementView
          key={section}
          section={section}
          port={port}
          auth={{ ...auth, state: auth.state }}
        />
      ) : (
        <p>Authentication is required.</p>
      )}
    </section>
  );
}
