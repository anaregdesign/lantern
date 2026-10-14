//! Complete current-authority cache partitions; no cached client-side grants.
use crate::{LanternError, generated::graph::v1::SecurityVersion};

pub(crate) fn validate_user_id(
    principal: &crate::generated::graph::v1::GetCurrentPrincipalResponse,
) -> Result<(), LanternError> {
    use crate::generated::graph::v1::SecurityPrincipalKind;
    let Some(id) = &principal.user_id else {
        return Ok(());
    };
    if id.len() != 32
        || !id
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        || id.bytes().all(|b| b == b'0')
        || principal.identity.as_ref().map(|v| v.kind) != Some(SecurityPrincipalKind::Oidc as i32)
    {
        return Err(LanternError::Protocol("invalid current user ID"));
    }
    Ok(())
}

/// Profile, complete cut, and credential/lineage commitment from one read.
/// Cancel old work and discard granting views/cursors when it changes. Retain
/// possibly dispatched mutation identities for status-only recovery.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CurrentAuthorityBinding(String);
impl CurrentAuthorityBinding {
    /// Stable complete partition key, never authority for a later RPC.
    pub fn as_str(&self) -> &str {
        &self.0
    }
    pub(crate) fn from_wire(v: SecurityVersion) -> Result<Self, LanternError> {
        let invalid = || LanternError::Protocol("invalid current authority contract");
        if v.revision != 0 || !v.digest.is_empty() || !v.generation.is_empty() {
            return Err(invalid());
        }
        let p = v.current_profile.ok_or_else(invalid)?;
        let c = v.current_cut.ok_or_else(invalid)?;
        if p.version != 2
            || c.version != 1
            || c.sequence == 0
            || p.domain != c.domain
            || p.cohort != c.cohort
            || p.generation != c.generation
        {
            return Err(invalid());
        }
        let bytes = |v: &[u8], size: usize, zero: bool| -> Result<String, LanternError> {
            if v.len() != size || !zero && !v.iter().any(|b| *b != 0) {
                return Err(invalid());
            }
            Ok(v.iter().map(|b| format!("{b:02x}")).collect())
        };
        let profile = [
            "current-v2".into(),
            bytes(&p.domain, 32, false)?,
            bytes(&p.cohort, 32, false)?,
            bytes(&p.generation, 16, false)?,
            bytes(&p.protocol, 32, false)?,
            bytes(&p.time_profile, 32, false)?,
            bytes(&p.membership, 32, false)?,
            bytes(&p.configuration, 32, false)?,
        ]
        .join(":");
        let cut = [
            "cut-v1".into(),
            bytes(&c.domain, 32, false)?,
            bytes(&c.cohort, 32, false)?,
            bytes(&c.generation, 16, false)?,
            c.sequence.to_string(),
            bytes(&c.previous, 32, true)?,
            bytes(&c.projection, 32, false)?,
            bytes(&c.frontier, 32, false)?,
            bytes(&c.fences, 32, false)?,
            bytes(&c.policy, 32, false)?,
        ]
        .join(":");
        Ok(Self(format!(
            "{profile}/{cut}/{}",
            bytes(&v.admission_binding, 32, false)?
        )))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::generated::graph::v1::{CurrentAuthorityProfile, CurrentSemanticCut};
    #[test]
    fn self_user_id_presence_and_invalid_machine_or_empty_values() {
        use crate::generated::graph::v1::{
            GetCurrentPrincipalResponse, SecurityIdentity, SecurityPrincipalKind,
        };
        let mut principal = GetCurrentPrincipalResponse {
            identity: Some(SecurityIdentity {
                kind: SecurityPrincipalKind::Oidc as i32,
                ..Default::default()
            }),
            ..Default::default()
        };
        for id in [Some("1".repeat(32)), None, Some("2".repeat(32))] {
            principal.user_id = id;
            assert!(validate_user_id(&principal).is_ok());
        }
        for id in [
            String::new(),
            "short".into(),
            "0".repeat(32),
            "A".repeat(32),
        ] {
            principal.user_id = Some(id);
            assert!(validate_user_id(&principal).is_err());
        }
        principal.identity.as_mut().unwrap().kind = SecurityPrincipalKind::Machine as i32;
        principal.user_id = None;
        assert!(validate_user_id(&principal).is_ok());
        principal.user_id = Some("1".repeat(32));
        assert!(validate_user_id(&principal).is_err());
    }
    fn version() -> SecurityVersion {
        SecurityVersion {
            current_profile: Some(CurrentAuthorityProfile {
                version: 2,
                domain: vec![1; 32],
                cohort: vec![2; 32],
                generation: vec![3; 16],
                protocol: vec![4; 32],
                time_profile: vec![5; 32],
                membership: vec![6; 32],
                configuration: vec![7; 32],
            }),
            current_cut: Some(CurrentSemanticCut {
                version: 1,
                domain: vec![1; 32],
                cohort: vec![2; 32],
                generation: vec![3; 16],
                sequence: 3,
                previous: vec![8; 32],
                projection: vec![9; 32],
                frontier: vec![10; 32],
                fences: vec![11; 32],
                policy: vec![12; 32],
            }),
            admission_binding: vec![13; 32],
            ..Default::default()
        }
    }
    #[test]
    fn complete_binding_refuses_legacy_and_covers_all_current_fields() {
        let original = CurrentAuthorityBinding::from_wire(version()).unwrap();
        let mut next = version();
        next.admission_binding[0] += 1;
        assert_ne!(original, CurrentAuthorityBinding::from_wire(next).unwrap());
        let mut next = version();
        next.current_cut.as_mut().unwrap().frontier[0] += 1;
        assert_ne!(original, CurrentAuthorityBinding::from_wire(next).unwrap());
        let mut next = version();
        next.current_profile.as_mut().unwrap().time_profile[0] += 1;
        assert_ne!(original, CurrentAuthorityBinding::from_wire(next).unwrap());
        let mut next = version();
        next.revision = 1;
        assert!(CurrentAuthorityBinding::from_wire(next).is_err());
        let mut next = version();
        next.current_cut.as_mut().unwrap().generation[0] += 1;
        assert!(CurrentAuthorityBinding::from_wire(next).is_err());
        let mut next = version();
        next.current_profile.as_mut().unwrap().version = 3;
        assert!(CurrentAuthorityBinding::from_wire(next).is_err());
        let mut next = version();
        next.admission_binding.clear();
        assert!(CurrentAuthorityBinding::from_wire(next).is_err());
    }
    #[tokio::test]
    #[ignore = "Run through the native public SDK4 root gate"]
    async fn real_public_current_wire() -> Result<(), Box<dyn std::error::Error>> {
        use crate::{CallOptions, LanternClient, TokenError, TokenProvider, VertexInput};
        use std::{future::Future, pin::Pin, sync::Arc};
        struct Credential(String);
        impl TokenProvider for Credential {
            fn token(
                &self,
            ) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
                Box::pin(async { Ok(self.0.clone()) })
            }
        }
        let sdk = LanternClient::builder(std::env::var("LANTERN_CURRENT_WIRE_URL")?)
            .tls_private_ca_pem(std::fs::read(std::env::var("LANTERN_CURRENT_WIRE_CA")?)?)?
            .token_provider(Arc::new(Credential(std::env::var(
                "LANTERN_CURRENT_WIRE_CREDENTIAL",
            )?)))
            .connect()
            .await?;
        let first = sdk
            .current_authority_binding(CallOptions::default())
            .await?;
        assert_eq!(
            sdk.current_user_id(CallOptions::default())
                .await?
                .as_deref(),
            Some("11111111111111111111111111111111")
        );
        assert!(first.as_str().starts_with("current-v2:"));
        assert!(first.as_str().contains("/cut-v1:"));
        assert_eq!(
            sdk.current_authority_binding(CallOptions::default())
                .await?,
            first
        );
        sdk.put_vertices([VertexInput::string("orders:rust-current", "current")])
            .await?;
        Ok(())
    }
}
