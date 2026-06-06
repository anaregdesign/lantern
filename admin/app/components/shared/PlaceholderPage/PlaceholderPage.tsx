import { MessageBar, MessageBarBody } from "@fluentui/react-components";
import styles from "./PlaceholderPage.module.css";

export interface PlaceholderPageProps {
  title: string;
  trackingIssue: string;
  description: string;
}

export function PlaceholderPage({
  title,
  trackingIssue,
  description,
}: PlaceholderPageProps) {
  return (
    <div className={styles.placeholder}>
      <h1 className={styles.title}>{title}</h1>
      <MessageBar intent="info" className={styles.message}>
        <MessageBarBody>
          {description} Coming in {trackingIssue}.
        </MessageBarBody>
      </MessageBar>
    </div>
  );
}
