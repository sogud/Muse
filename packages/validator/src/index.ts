import type {
  DialogueNode,
  EpisodeDefinition,
  LearningItem,
  LocaleBundle,
  NarrationNode,
  SceneDefinition,
  StoryDefinition,
  StoryNode
} from '@aivn/story-schema'

export type DiagnosticSeverity = 'error' | 'warning' | 'info'

export type ValidationDiagnostic = {
  severity: DiagnosticSeverity
  code: string
  message: string
  path: string
}

export type ValidationAsset = {
  assetKey?: string
  key?: string
  id?: string
  storageKey?: string
}

export type StoryValidationContent = {
  story: StoryDefinition
  episodes: EpisodeDefinition[]
  targetLocale: LocaleBundle
  nativeLocale?: LocaleBundle
  learningItems?: LearningItem[]
  assets?: ValidationAsset[]
}

export type ValidationOptions = {
  maxLearningPointsPerEpisode?: number
  maxDialogueCharacters?: number
}

type NodeRef = {
  episode: EpisodeDefinition
  scene: SceneDefinition
  node: StoryNode
}

const DEFAULT_MAX_LEARNING_POINTS = 8
const DEFAULT_MAX_DIALOGUE_CHARACTERS = 160

export const validateStoryContent = (
  content: StoryValidationContent,
  options: ValidationOptions = {}
): ValidationDiagnostic[] => {
  const diagnostics: ValidationDiagnostic[] = []
  const maxLearningPoints = options.maxLearningPointsPerEpisode ?? DEFAULT_MAX_LEARNING_POINTS
  const maxDialogueCharacters = options.maxDialogueCharacters ?? DEFAULT_MAX_DIALOGUE_CHARACTERS
  const episodeById = new Map<string, EpisodeDefinition>()
  const nodeById = new Map<string, NodeRef>()
  const learningIds = new Set((content.learningItems ?? []).map((item) => item.id))
  const assetKeys = collectAssetKeys(content.assets ?? [])

  collectEpisodeAndNodeIndexes(content.episodes, episodeById, nodeById, diagnostics)
  validateStoryRefs(content.story, episodeById, diagnostics)
  validateEpisodeRefs(content, episodeById, nodeById, learningIds, assetKeys, diagnostics, {
    maxLearningPoints,
    maxDialogueCharacters
  })

  return diagnostics
}

const collectEpisodeAndNodeIndexes = (
  episodes: EpisodeDefinition[],
  episodeById: Map<string, EpisodeDefinition>,
  nodeById: Map<string, NodeRef>,
  diagnostics: ValidationDiagnostic[]
) => {
  const sceneIds = new Set<string>()

  for (const episode of episodes) {
    const episodePath = `episodes.${episode.episodeId}`

    if (episodeById.has(episode.episodeId)) {
      diagnostics.push(error('DUPLICATE_EPISODE_ID', `Duplicate episode id "${episode.episodeId}".`, episodePath))
    }

    episodeById.set(episode.episodeId, episode)

    for (const scene of episode.scenes) {
      const scenePath = `${episodePath}.scenes.${scene.id}`

      if (sceneIds.has(scene.id)) {
        diagnostics.push(error('DUPLICATE_SCENE_ID', `Duplicate scene id "${scene.id}".`, scenePath))
      }

      sceneIds.add(scene.id)

      for (const node of scene.nodes) {
        const nodePath = `${scenePath}.nodes.${node.id}`

        if (nodeById.has(node.id)) {
          diagnostics.push(error('DUPLICATE_NODE_ID', `Duplicate node id "${node.id}".`, nodePath))
        }

        nodeById.set(node.id, { episode, scene, node })
      }
    }
  }
}

const validateStoryRefs = (
  story: StoryDefinition,
  episodeById: Map<string, EpisodeDefinition>,
  diagnostics: ValidationDiagnostic[]
) => {
  if (!episodeById.has(story.startEpisodeId)) {
    diagnostics.push(
      error(
        'MISSING_START_EPISODE',
        `Story start episode "${story.startEpisodeId}" does not exist.`,
        'story.startEpisodeId'
      )
    )
  }

  const storyEpisodeIds = new Set<string>()

  for (const episodeRef of story.episodes) {
    const episodePath = `story.episodes.${episodeRef.id}`

    if (storyEpisodeIds.has(episodeRef.id)) {
      diagnostics.push(error('DUPLICATE_STORY_EPISODE_REF', `Duplicate story episode ref "${episodeRef.id}".`, episodePath))
    }

    storyEpisodeIds.add(episodeRef.id)

    if (!episodeById.has(episodeRef.id)) {
      diagnostics.push(error('MISSING_EPISODE', `Story references missing episode "${episodeRef.id}".`, episodePath))
    }
  }
}

const validateEpisodeRefs = (
  content: StoryValidationContent,
  episodeById: Map<string, EpisodeDefinition>,
  nodeById: Map<string, NodeRef>,
  learningIds: Set<string>,
  assetKeys: Set<string>,
  diagnostics: ValidationDiagnostic[],
  limits: {
    maxLearningPoints: number
    maxDialogueCharacters: number
  }
) => {
  for (const episode of episodeById.values()) {
    const episodePath = `episodes.${episode.episodeId}`
    const episodeLearningIds = new Set<string>()
    let hasChoice = false

    validateTextKey(episode.titleKey, `${episodePath}.titleKey`, content, diagnostics)
    if (episode.summaryKey) validateTextKey(episode.summaryKey, `${episodePath}.summaryKey`, content, diagnostics)
    if (episode.cliffhangerKey) {
      validateTextKey(episode.cliffhangerKey, `${episodePath}.cliffhangerKey`, content, diagnostics)
    } else {
      diagnostics.push(warning('MISSING_CLIFFHANGER', `Episode "${episode.episodeId}" has no cliffhanger key.`, episodePath))
    }

    for (const learningId of episode.targetExpressionIds) {
      episodeLearningIds.add(learningId)
      validateLearningId(learningId, `${episodePath}.targetExpressionIds.${learningId}`, learningIds, diagnostics)
    }

    for (const scene of episode.scenes) {
      const scenePath = `${episodePath}.scenes.${scene.id}`
      const sceneNodeIds = new Set(scene.nodes.map((node) => node.id))
      const reachableNodeIds = collectReachableNodeIds(scene, nodeById)

      if (!sceneNodeIds.has(scene.startNodeId)) {
        diagnostics.push(
          error('MISSING_START_NODE', `Scene "${scene.id}" start node "${scene.startNodeId}" does not exist.`, scenePath)
        )
      }

      if (scene.background) validateAssetRef(scene.background, `${scenePath}.background`, assetKeys, diagnostics)
      if (scene.bgm) validateAssetRef(scene.bgm, `${scenePath}.bgm`, assetKeys, diagnostics)

      for (const node of scene.nodes) {
        const nodePath = `${scenePath}.nodes.${node.id}`
        if (node.type === 'choice') hasChoice = true

        validateNode(
          node,
          nodePath,
          content,
          nodeById,
          learningIds,
          assetKeys,
          diagnostics,
          limits.maxDialogueCharacters
        )

        for (const learningId of getNodeLearningIds(node)) {
          episodeLearningIds.add(learningId)
        }

        if (!reachableNodeIds.has(node.id)) {
          diagnostics.push(
            warning('UNREACHABLE_NODE', `Node "${node.id}" is not reachable from scene start.`, nodePath)
          )
        }
      }

      if (!canReachEnd(scene.startNodeId, nodeById)) {
        diagnostics.push(warning('NO_END_REACHABLE', `Scene "${scene.id}" has no reachable end node.`, scenePath))
      }
    }

    if (!hasChoice) {
      diagnostics.push(warning('NO_CHOICE', `Episode "${episode.episodeId}" has no choice node.`, episodePath))
    }

    if (episodeLearningIds.size > limits.maxLearningPoints) {
      diagnostics.push(
        warning(
          'TOO_MANY_LEARNING_POINTS',
          `Episode "${episode.episodeId}" has ${episodeLearningIds.size} learning points. Recommended maximum is ${limits.maxLearningPoints}.`,
          `${episodePath}.targetExpressionIds`
        )
      )
    }
  }
}

const validateNode = (
  node: StoryNode,
  nodePath: string,
  content: StoryValidationContent,
  nodeById: Map<string, NodeRef>,
  learningIds: Set<string>,
  assetKeys: Set<string>,
  diagnostics: ValidationDiagnostic[],
  maxDialogueCharacters: number
) => {
  if (node.type === 'dialogue') {
    validateLineNode(node, nodePath, content, learningIds, assetKeys, diagnostics, maxDialogueCharacters)
    validateOptionalNext(node.next, `${nodePath}.next`, nodeById, diagnostics)
    if (node.sprite) validateAssetRef(node.sprite, `${nodePath}.sprite`, assetKeys, diagnostics)
    if (node.voice) validateAssetRef(node.voice, `${nodePath}.voice`, assetKeys, diagnostics)
    return
  }

  if (node.type === 'narration') {
    validateLineNode(node, nodePath, content, learningIds, assetKeys, diagnostics, maxDialogueCharacters)
    validateOptionalNext(node.next, `${nodePath}.next`, nodeById, diagnostics)
    if (node.voice) validateAssetRef(node.voice, `${nodePath}.voice`, assetKeys, diagnostics)
    return
  }

  if (node.type === 'choice') {
    validateTextKey(node.promptKey, `${nodePath}.promptKey`, content, diagnostics)
    for (const choice of node.choices) {
      const choicePath = `${nodePath}.choices.${choice.id}`
      validateTextKey(choice.textKey, `${choicePath}.textKey`, content, diagnostics)
      validateRequiredNext(choice.next, `${choicePath}.next`, nodeById, diagnostics)
    }
    return
  }

  if (node.type === 'setVariable') {
    validateRequiredNext(node.next, `${nodePath}.next`, nodeById, diagnostics)
    return
  }

  if (node.type === 'conditional') {
    for (const condition of node.conditions) {
      validateRequiredNext(condition.next, `${nodePath}.conditions.${condition.variable}.next`, nodeById, diagnostics)
    }
    validateRequiredNext(node.defaultNext, `${nodePath}.defaultNext`, nodeById, diagnostics)
    return
  }

  if (node.type === 'jump') {
    validateRequiredNext(node.next, `${nodePath}.next`, nodeById, diagnostics)
    return
  }

  if (node.endingKey) {
    validateTextKey(node.endingKey, `${nodePath}.endingKey`, content, diagnostics)
  }
}

const validateLineNode = (
  node: DialogueNode | NarrationNode,
  nodePath: string,
  content: StoryValidationContent,
  learningIds: Set<string>,
  assetKeys: Set<string>,
  diagnostics: ValidationDiagnostic[],
  maxDialogueCharacters: number
) => {
  validateTextKey(node.textKey, `${nodePath}.textKey`, content, diagnostics)

  const text = content.targetLocale.entries[node.textKey]
  if (text !== undefined && text.trim().length === 0) {
    diagnostics.push(error('EMPTY_DIALOGUE', `Text key "${node.textKey}" is empty.`, `${nodePath}.textKey`))
  }

  if (text !== undefined && text.length > maxDialogueCharacters) {
    diagnostics.push(
      warning(
        'OVERLY_LONG_DIALOGUE',
        `Text key "${node.textKey}" is ${text.length} characters. Recommended maximum is ${maxDialogueCharacters}.`,
        `${nodePath}.textKey`
      )
    )
  }

  for (const learningId of node.learningPointIds) {
    validateLearningId(learningId, `${nodePath}.learningPointIds.${learningId}`, learningIds, diagnostics)
  }

  if (node.type === 'narration' && node.voice) {
    validateAssetRef(node.voice, `${nodePath}.voice`, assetKeys, diagnostics)
  }
}

const validateTextKey = (
  textKey: string,
  path: string,
  content: StoryValidationContent,
  diagnostics: ValidationDiagnostic[]
) => {
  if (!(textKey in content.targetLocale.entries)) {
    diagnostics.push(error('MISSING_TARGET_TEXT_KEY', `Missing target locale key "${textKey}".`, path))
  }

  if (content.nativeLocale && !(textKey in content.nativeLocale.entries)) {
    diagnostics.push(error('MISSING_NATIVE_TEXT_KEY', `Missing native locale key "${textKey}".`, path))
  }
}

const validateLearningId = (
  learningId: string,
  path: string,
  learningIds: Set<string>,
  diagnostics: ValidationDiagnostic[]
) => {
  if (!learningIds.has(learningId)) {
    diagnostics.push(error('MISSING_LEARNING_ITEM', `Missing learning item "${learningId}".`, path))
  }
}

const validateAssetRef = (
  assetRef: string,
  path: string,
  assetKeys: Set<string>,
  diagnostics: ValidationDiagnostic[]
) => {
  if (assetKeys.size > 0 && !assetKeys.has(assetRef)) {
    diagnostics.push(error('MISSING_ASSET', `Missing asset "${assetRef}".`, path))
  }
}

const validateRequiredNext = (
  nextNodeId: string,
  path: string,
  nodeById: Map<string, NodeRef>,
  diagnostics: ValidationDiagnostic[]
) => {
  if (!nodeById.has(nextNodeId)) {
    diagnostics.push(error('MISSING_NEXT_NODE', `Next node "${nextNodeId}" does not exist.`, path))
  }
}

const validateOptionalNext = (
  nextNodeId: string | undefined,
  path: string,
  nodeById: Map<string, NodeRef>,
  diagnostics: ValidationDiagnostic[]
) => {
  if (!nextNodeId) {
    diagnostics.push(warning('MISSING_NEXT', 'Renderable node has no next node.', path))
    return
  }

  validateRequiredNext(nextNodeId, path, nodeById, diagnostics)
}

const collectReachableNodeIds = (scene: SceneDefinition, nodeById: Map<string, NodeRef>): Set<string> => {
  const reachable = new Set<string>()
  const stack = [scene.startNodeId]

  while (stack.length > 0) {
    const nodeId = stack.pop()
    if (!nodeId || reachable.has(nodeId)) continue

    reachable.add(nodeId)
    const node = nodeById.get(nodeId)?.node
    if (!node) continue

    for (const nextNodeId of getNextNodeIds(node)) {
      stack.push(nextNodeId)
    }
  }

  return reachable
}

const canReachEnd = (startNodeId: string, nodeById: Map<string, NodeRef>): boolean => {
  const visited = new Set<string>()
  const stack = [startNodeId]

  while (stack.length > 0) {
    const nodeId = stack.pop()
    if (!nodeId || visited.has(nodeId)) continue

    visited.add(nodeId)
    const node = nodeById.get(nodeId)?.node
    if (!node) continue

    if (node.type === 'end') {
      return true
    }

    for (const nextNodeId of getNextNodeIds(node)) {
      stack.push(nextNodeId)
    }
  }

  return false
}

const getNextNodeIds = (node: StoryNode): string[] => {
  if (node.type === 'dialogue' || node.type === 'narration') {
    return node.next ? [node.next] : []
  }

  if (node.type === 'choice') {
    return node.choices.map((choice) => choice.next)
  }

  if (node.type === 'setVariable' || node.type === 'jump') {
    return [node.next]
  }

  if (node.type === 'conditional') {
    return [...node.conditions.map((condition) => condition.next), node.defaultNext]
  }

  return []
}

const getNodeLearningIds = (node: StoryNode): string[] => {
  if (node.type === 'dialogue' || node.type === 'narration') {
    return node.learningPointIds
  }

  return []
}

const collectAssetKeys = (assets: ValidationAsset[]): Set<string> => {
  const assetKeys = new Set<string>()

  for (const asset of assets) {
    for (const candidate of [asset.assetKey, asset.key, asset.id, asset.storageKey]) {
      if (candidate) {
        assetKeys.add(candidate)
      }
    }
  }

  return assetKeys
}

const error = (code: string, message: string, path: string): ValidationDiagnostic => ({
  severity: 'error',
  code,
  message,
  path
})

const warning = (code: string, message: string, path: string): ValidationDiagnostic => ({
  severity: 'warning',
  code,
  message,
  path
})

export const hasValidationErrors = (diagnostics: ValidationDiagnostic[]): boolean =>
  diagnostics.some((diagnostic) => diagnostic.severity === 'error')
