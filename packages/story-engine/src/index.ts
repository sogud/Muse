import type {
  ChoiceOption,
  EpisodeDefinition,
  LearningItem,
  LocaleBundle,
  SceneDefinition,
  StoryDefinition,
  StoryNode,
  VariableMap,
  VariableValue
} from '@aivn/story-schema'

export type AssetRef = {
  id: string
}

export type StoryHistoryItem = {
  episodeId: string
  sceneId: string
  nodeId: string
  eventType: EngineEvent['type']
}

export type StoryState = {
  storyId: string
  episodeId: string
  sceneId: string
  nodeId: string
  variables: VariableMap
  history: StoryHistoryItem[]
  seenLearningPointIds: string[]
}

export type EngineEvent =
  | { type: 'START'; storyId: string }
  | { type: 'NEXT' }
  | { type: 'CHOOSE'; choiceId: string }
  | { type: 'JUMP'; nodeId: string }
  | { type: 'RESET' }

export type LearningEvent = {
  type: 'SEEN'
  itemId: string
  nodeId: string
}

export type EngineEffect =
  | {
      type: 'VARIABLE_SET'
      key: string
      value: VariableValue
    }
  | {
      type: 'CHOICE_SELECTED'
      choiceId: string
    }
  | {
      type: 'STORY_END'
      nodeId: string
    }

export type RenderChoice = {
  id: string
  text: string
  translation?: string
  level?: 'easy' | 'natural' | 'expressive'
}

export type RenderModel = {
  background?: AssetRef
  bgm?: AssetRef
  speaker?: string
  characterSprite?: AssetRef
  dialogue?: {
    nodeId: string
    text: string
    translation?: string
    voice?: AssetRef
    learningPointIds: string[]
  }
  choices?: RenderChoice[]
  end?: {
    nodeId: string
    text?: string
  }
}

export type StoryContent = {
  story: StoryDefinition
  episodes: EpisodeDefinition[]
  targetLocale: LocaleBundle
  nativeLocale?: LocaleBundle
  learningItems?: LearningItem[]
}

export type EngineResult = {
  state: StoryState
  renderModel: RenderModel
  learningEvents: LearningEvent[]
  effects: EngineEffect[]
}

type NodeLocation = {
  episode: EpisodeDefinition
  scene: SceneDefinition
  node: StoryNode
}

type StoryIndex = {
  episodes: Map<string, EpisodeDefinition>
  nodes: Map<string, NodeLocation>
}

const MAX_INTERNAL_STEPS = 100

export class StoryEngineError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'StoryEngineError'
  }
}

export const createInitialState = (content: StoryContent): StoryState => {
  const index = createStoryIndex(content)
  const entry = resolveEntryNode(content, index)

  return {
    storyId: content.story.storyId,
    episodeId: entry.episode.episodeId,
    sceneId: entry.scene.id,
    nodeId: entry.node.id,
    variables: { ...content.story.variables },
    history: [],
    seenLearningPointIds: []
  }
}

export const runStoryEvent = (
  content: StoryContent,
  state: StoryState | undefined,
  event: EngineEvent
): EngineResult => {
  const index = createStoryIndex(content)
  const startState = event.type === 'START' || event.type === 'RESET' ? createInitialState(content) : state

  if (!startState) {
    throw new StoryEngineError('Story state is required before non-START events')
  }

  const effects: EngineEffect[] = []
  const nextState = applyExternalEvent(content, index, startState, event, effects)
  const resolvedState = resolveInternalNodes(content, index, nextState, event.type, effects)
  const renderModel = createRenderModel(content, index, resolvedState)
  const learningEvents = collectLearningEvents(index, resolvedState)
  const stateWithLearning = applyLearningEvents(resolvedState, learningEvents)

  return {
    state: stateWithLearning,
    renderModel,
    learningEvents,
    effects
  }
}

const createStoryIndex = (content: StoryContent): StoryIndex => {
  const episodes = new Map<string, EpisodeDefinition>()
  const nodes = new Map<string, NodeLocation>()

  for (const episode of content.episodes) {
    episodes.set(episode.episodeId, episode)

    for (const scene of episode.scenes) {
      for (const node of scene.nodes) {
        nodes.set(node.id, {
          episode,
          scene,
          node
        })
      }
    }
  }

  return { episodes, nodes }
}

const resolveEntryNode = (content: StoryContent, index: StoryIndex): NodeLocation => {
  const episodeRef = content.story.episodes.find((episode) => episode.id === content.story.startEpisodeId)
  const episode = index.episodes.get(content.story.startEpisodeId)

  if (!episodeRef || !episode) {
    throw new StoryEngineError(`Missing start episode: ${content.story.startEpisodeId}`)
  }

  const firstSceneId = episodeRef.sceneIds[0] ?? episode.scenes[0]?.id
  const scene = episode.scenes.find((candidate) => candidate.id === firstSceneId)

  if (!scene) {
    throw new StoryEngineError(`Missing start scene: ${firstSceneId}`)
  }

  const node = scene.nodes.find((candidate) => candidate.id === scene.startNodeId)

  if (!node) {
    throw new StoryEngineError(`Missing start node: ${scene.startNodeId}`)
  }

  return { episode, scene, node }
}

const applyExternalEvent = (
  content: StoryContent,
  index: StoryIndex,
  state: StoryState,
  event: EngineEvent,
  effects: EngineEffect[]
): StoryState => {
  if (event.type === 'START' || event.type === 'RESET') {
    return state
  }

  const location = getLocation(index, state.nodeId)
  const history = [...state.history, createHistoryItem(state, event.type)]

  if (event.type === 'JUMP') {
    return stateAtNode(index, state, event.nodeId, history)
  }

  if (event.type === 'NEXT') {
    if (location.node.type === 'dialogue' || location.node.type === 'narration') {
      if (!location.node.next) {
        return state
      }

      return stateAtNode(index, state, location.node.next, history)
    }

    return state
  }

  if (event.type === 'CHOOSE') {
    if (location.node.type !== 'choice') {
      throw new StoryEngineError(`Cannot choose from node type: ${location.node.type}`)
    }

    const choice = location.node.choices.find((candidate) => candidate.id === event.choiceId)

    if (!choice) {
      throw new StoryEngineError(`Missing choice: ${event.choiceId}`)
    }

    effects.push({ type: 'CHOICE_SELECTED', choiceId: choice.id })
    const variables = applyEffects(state.variables, choice, effects)

    return stateAtNode(index, { ...state, variables }, choice.next, history)
  }

  return content.story.storyId === state.storyId ? state : createInitialState(content)
}

const resolveInternalNodes = (
  content: StoryContent,
  index: StoryIndex,
  state: StoryState,
  eventType: EngineEvent['type'],
  effects: EngineEffect[]
): StoryState => {
  let current = state

  for (let step = 0; step < MAX_INTERNAL_STEPS; step += 1) {
    const location = getLocation(index, current.nodeId)

    if (location.node.type === 'setVariable') {
      const variables = applyVariableMap(current.variables, location.node.effects, effects)
      current = stateAtNode(index, { ...current, variables }, location.node.next, [
        ...current.history,
        createHistoryItem(current, eventType)
      ])
      continue
    }

    if (location.node.type === 'conditional') {
      const branch = location.node.conditions.find((condition) =>
        compareVariable(current.variables[condition.variable], condition.operator, condition.value)
      )
      const nextNodeId = branch?.next ?? location.node.defaultNext
      current = stateAtNode(index, current, nextNodeId, [
        ...current.history,
        createHistoryItem(current, eventType)
      ])
      continue
    }

    if (location.node.type === 'jump') {
      current = stateAtNode(index, current, location.node.next, [
        ...current.history,
        createHistoryItem(current, eventType)
      ])
      continue
    }

    if (location.node.type === 'end') {
      effects.push({ type: 'STORY_END', nodeId: location.node.id })
    }

    return current
  }

  throw new StoryEngineError(
    `Exceeded ${MAX_INTERNAL_STEPS} internal story transitions in ${content.story.storyId}`
  )
}

const createRenderModel = (
  content: StoryContent,
  index: StoryIndex,
  state: StoryState
): RenderModel => {
  const location = getLocation(index, state.nodeId)
  const shared = {
    background: location.scene.background ? toAssetRef(location.scene.background) : undefined,
    bgm: location.scene.bgm ? toAssetRef(location.scene.bgm) : undefined
  }

  if (location.node.type === 'dialogue') {
    return {
      ...shared,
      speaker: location.node.speaker,
      characterSprite: location.node.sprite ? toAssetRef(location.node.sprite) : undefined,
      dialogue: {
        nodeId: location.node.id,
        text: readText(content.targetLocale, location.node.textKey),
        translation: readOptionalText(content.nativeLocale, location.node.textKey),
        voice: location.node.voice ? toAssetRef(location.node.voice) : undefined,
        learningPointIds: location.node.learningPointIds
      }
    }
  }

  if (location.node.type === 'narration') {
    return {
      ...shared,
      dialogue: {
        nodeId: location.node.id,
        text: readText(content.targetLocale, location.node.textKey),
        translation: readOptionalText(content.nativeLocale, location.node.textKey),
        voice: location.node.voice ? toAssetRef(location.node.voice) : undefined,
        learningPointIds: location.node.learningPointIds
      }
    }
  }

  if (location.node.type === 'choice') {
    return {
      ...shared,
      dialogue: {
        nodeId: location.node.id,
        text: readText(content.targetLocale, location.node.promptKey),
        translation: readOptionalText(content.nativeLocale, location.node.promptKey),
        learningPointIds: []
      },
      choices: location.node.choices.map((choice) => ({
        id: choice.id,
        text: readText(content.targetLocale, choice.textKey),
        translation: readOptionalText(content.nativeLocale, choice.textKey),
        level: choice.level
      }))
    }
  }

  if (location.node.type === 'end') {
    return {
      ...shared,
      end: {
        nodeId: location.node.id,
        text: location.node.endingKey ? readOptionalText(content.targetLocale, location.node.endingKey) : undefined
      }
    }
  }

  throw new StoryEngineError(`Cannot render internal node type: ${location.node.type}`)
}

const collectLearningEvents = (index: StoryIndex, state: StoryState): LearningEvent[] => {
  const location = getLocation(index, state.nodeId)
  const learningPointIds =
    location.node.type === 'dialogue' || location.node.type === 'narration'
      ? location.node.learningPointIds
      : []

  return learningPointIds
    .filter((itemId) => !state.seenLearningPointIds.includes(itemId))
    .map((itemId) => ({
      type: 'SEEN',
      itemId,
      nodeId: location.node.id
    }))
}

const applyLearningEvents = (state: StoryState, learningEvents: LearningEvent[]): StoryState => {
  if (learningEvents.length === 0) {
    return state
  }

  return {
    ...state,
    seenLearningPointIds: [
      ...state.seenLearningPointIds,
      ...learningEvents.map((learningEvent) => learningEvent.itemId)
    ]
  }
}

const stateAtNode = (
  index: StoryIndex,
  state: StoryState,
  nodeId: string,
  history: StoryHistoryItem[]
): StoryState => {
  const location = getLocation(index, nodeId)

  return {
    ...state,
    episodeId: location.episode.episodeId,
    sceneId: location.scene.id,
    nodeId,
    history
  }
}

const getLocation = (index: StoryIndex, nodeId: string): NodeLocation => {
  const location = index.nodes.get(nodeId)

  if (!location) {
    throw new StoryEngineError(`Missing node: ${nodeId}`)
  }

  return location
}

const createHistoryItem = (
  state: StoryState,
  eventType: EngineEvent['type']
): StoryHistoryItem => ({
  episodeId: state.episodeId,
  sceneId: state.sceneId,
  nodeId: state.nodeId,
  eventType
})

const applyEffects = (
  variables: VariableMap,
  choice: ChoiceOption,
  effects: EngineEffect[]
): VariableMap => applyVariableMap(variables, choice.effects ?? {}, effects)

const applyVariableMap = (
  variables: VariableMap,
  changes: VariableMap,
  effects: EngineEffect[]
): VariableMap => {
  const nextVariables = { ...variables }

  for (const [key, value] of Object.entries(changes)) {
    nextVariables[key] = value
    effects.push({ type: 'VARIABLE_SET', key, value })
  }

  return nextVariables
}

const compareVariable = (
  current: VariableValue | undefined,
  operator: 'eq' | 'neq' | 'gt' | 'gte' | 'lt' | 'lte',
  expected: VariableValue
): boolean => {
  if (operator === 'eq') {
    return current === expected
  }

  if (operator === 'neq') {
    return current !== expected
  }

  if (typeof current !== 'number' || typeof expected !== 'number') {
    return false
  }

  if (operator === 'gt') {
    return current > expected
  }

  if (operator === 'gte') {
    return current >= expected
  }

  if (operator === 'lt') {
    return current < expected
  }

  return current <= expected
}

const toAssetRef = (id: string): AssetRef => ({ id })

const readText = (locale: LocaleBundle, key: string): string => locale.entries[key] ?? key

const readOptionalText = (locale: LocaleBundle | undefined, key: string): string | undefined =>
  locale?.entries[key]
