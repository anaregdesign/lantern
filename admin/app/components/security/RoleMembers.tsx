import { Button, Title3 } from "@fluentui/react-components";
import { Link } from "react-router";
import type { SecurityUser } from "lantern-sdk/web";
import styles from "./RoleMembers.module.css";

export function RoleMembers({
  roleId,
  members,
  cursor,
  disabled,
  load,
}: {
  roleId: string;
  members: SecurityUser[];
  cursor: string;
  disabled: boolean;
  load(cursor?: string): void;
}) {
  return (
    <section className={styles.panel} aria-label="Role members">
      <Title3>Users assigned to {roleId}</Title3>
      <p>
        Inspect one user page at a time. These matches do not represent a total
        member count.
      </p>
      <Button disabled={disabled} onClick={() => load()}>
        Inspect first user page
      </Button>
      <ul>
        {members.map((user) => (
          <li key={JSON.stringify(user.identity)}>
            {user.identity?.subject || user.identity?.machineName} ·{" "}
            {user.identity?.issuer}
          </li>
        ))}
      </ul>
      {cursor && (
        <Button disabled={disabled} onClick={() => load(cursor)}>
          Inspect next user page
        </Button>
      )}
      <Link to="/security/users">Manage user memberships</Link>
    </section>
  );
}
