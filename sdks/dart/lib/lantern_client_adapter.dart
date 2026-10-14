/// Parent-owned no-retry indexed attempt seam for qualified coordination adapters.
///
/// Ordinary applications use `lantern_client.dart`. This library keeps generated
/// messages private and adds no storage, retry owner or offline dependency.
library;

export 'src/client.dart'
    show
        IndexedMutationIntent,
        IndexedMutationAttemptException,
        LanternIndexedMutations,
        LanternObservationCoordinator;
