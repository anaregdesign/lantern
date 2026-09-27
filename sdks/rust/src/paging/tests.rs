use std::{
    future::pending,
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
};

use super::*;

#[tokio::test]
async fn lazy_pages_do_not_prefetch_and_yield_terminal_error_after_hits() {
    let calls = Arc::new(AtomicUsize::new(0));
    let observed = calls.clone();
    let mut stream = page_stream(0_u8, move |cursor| {
        let calls = observed.clone();
        async move {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok(PageItems {
                items: if cursor == 0 { vec![1, 2] } else { vec![3] },
                next_cursor: if cursor == 0 { Some(1) } else { None },
                terminal_error: if cursor == 0 {
                    Some(LanternError::SearchContinuationLimited)
                } else {
                    None
                },
            })
        }
    });
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    assert_eq!(stream.next().await.unwrap().unwrap(), 1);
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    assert_eq!(stream.next().await.unwrap().unwrap(), 2);
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    assert_eq!(stream.next().await.unwrap().unwrap(), 3);
    assert_eq!(calls.load(Ordering::SeqCst), 2);
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(stream.next().await.is_none());
    assert_eq!(calls.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn dropping_stream_cancels_active_fetch() {
    struct Guard(Arc<AtomicBool>);
    impl Drop for Guard {
        fn drop(&mut self) {
            self.0.store(true, Ordering::SeqCst);
        }
    }
    let stopped = Arc::new(AtomicBool::new(false));
    let ready = Arc::new(tokio::sync::Notify::new());
    let fetching = stopped.clone();
    let started = ready.clone();
    let mut stream = page_stream(0_u8, move |_| {
        let guard = Guard(fetching.clone());
        let ready = started.clone();
        async move {
            ready.notify_one();
            let _guard = guard;
            pending::<()>().await;
            Ok::<_, LanternError>(PageItems::<u8, u8> {
                items: Vec::new(),
                next_cursor: None,
                terminal_error: None,
            })
        }
    });
    let task = tokio::spawn(async move { stream.next().await });
    ready.notified().await;
    task.abort();
    let _ = task.await;
    assert!(stopped.load(Ordering::SeqCst));
}

#[tokio::test]
async fn malformed_empty_or_repeating_page_cannot_spin() {
    for (items, cursor) in [(Vec::<u8>::new(), 1), (vec![3], 0)] {
        let mut stream = page_stream(0_u8, move |_| {
            let items = items.clone();
            async move {
                Ok(PageItems {
                    items,
                    next_cursor: Some(cursor),
                    terminal_error: None,
                })
            }
        });
        assert!(matches!(
            stream.next().await,
            Some(Err(LanternError::Protocol(_)))
        ));
        assert!(stream.next().await.is_none());
    }
}
