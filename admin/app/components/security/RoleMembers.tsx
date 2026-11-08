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
  select,
}: {
  roleId: string;
  members: SecurityUser[];
  cursor: string;
  disabled: boolean;
  load(cursor?: string): void;
  select(user: SecurityUser): void;
}) {
  return (
    <section className={styles.panel} aria-label="Role members">
      <Title3>Assign members to {roleId}</Title3>
      <p>
        Inspect one user page at a time. Choose a user to manage saved Role
        assignments through the same Review/Apply flow. Creating a Role alone
        grants no user access. This page is not a total member count.
      </p>
      <Button disabled={disabled} onClick={() => load()}>
        Inspect first user page
      </Button>
      <ul>
        {members.map((user) => (
          <li key={JSON.stringify(user.identity)}>
            {user.verifiedEmail ||
              user.identity?.subject ||
              user.identity?.machineName}{" "}
            · {user.identity?.issuer}
            {user.assignments.some((assignment) => assignment.roleId === roleId)
              ? " · assigned"
              : " · not assigned"}
            <Button disabled={disabled} onClick={() => select(user)}>
              Manage Roles for{" "}
              {user.verifiedEmail ||
                user.identity?.subject ||
                user.identity?.machineName}
            </Button>
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
