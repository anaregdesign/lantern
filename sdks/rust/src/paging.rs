use std::{collections::VecDeque, future::Future};

use futures_util::{
    StreamExt,
    stream::{self, BoxStream},
};

use crate::LanternError;

/// A lazy, `Send` stream of validated items. At most one page is buffered;
/// dropping the stream cancels any active unary page future.
pub type QueryStream<T> = BoxStream<'static, Result<T, LanternError>>;

/// Server continuation and an optional terminal error that must follow all
/// retained items, even if the final page has no next cursor.
pub(crate) struct PageItems<T, C> {
    pub items: Vec<T>,
    pub next_cursor: Option<C>,
    pub terminal_error: Option<LanternError>,
}

struct PageState<T, C, F> {
    fetch: F,
    cursor: C,
    items: VecDeque<T>,
    terminal_error: Option<LanternError>,
    finished: bool,
}

pub(crate) fn page_stream<T, C, F, Fut>(cursor: C, fetch: F) -> QueryStream<T>
where
    T: Send + 'static,
    C: Clone + Eq + Send + 'static,
    F: Fn(C) -> Fut + Send + Sync + 'static,
    Fut: Future<Output = Result<PageItems<T, C>, LanternError>> + Send + 'static,
{
    stream::try_unfold(
        PageState {
            fetch,
            cursor,
            items: VecDeque::new(),
            terminal_error: None,
            finished: false,
        },
        |mut state| async move {
            loop {
                if let Some(item) = state.items.pop_front() {
                    return Ok(Some((item, state)));
                }
                if state.finished {
                    if let Some(error) = state.terminal_error.take() {
                        return Err(error);
                    }
                    return Ok(None);
                }
                let page = (state.fetch)(state.cursor.clone()).await?;
                if page.items.is_empty() && page.next_cursor.is_some() {
                    return Err(LanternError::Protocol("empty query page cannot continue"));
                }
                if let Some(next) = page.next_cursor {
                    if next == state.cursor {
                        return Err(LanternError::Protocol("query page repeated its cursor"));
                    }
                    state.cursor = next;
                } else {
                    state.finished = true;
                }
                if let Some(error) = page.terminal_error {
                    state.terminal_error = Some(error);
                }
                state.items = page.items.into();
            }
        },
    )
    .boxed()
}

#[cfg(test)]
mod tests;
