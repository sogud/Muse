import { describe, expect, it } from 'vitest'
import {
  ChoiceNodeSchema,
  EpisodeDefinitionSchema,
  LearningItemSchema,
  ProjectManifestSchema,
  StoryDefinitionSchema
} from '../src/index'

describe('story-schema', () => {
  it('parses a valid project manifest', () => {
    const result = ProjectManifestSchema.safeParse({
      projectId: 'midnight-cafe',
      title: {
        'en-US': 'Midnight Cafe',
        'zh-CN': '午夜咖啡馆'
      },
      version: '0.1.0',
      engineVersion: '0.1.0',
      targetLanguage: 'en-US',
      nativeLanguage: 'zh-CN',
      level: 'A2',
      contentRating: 'teen',
      createdAt: '2026-05-24T00:00:00.000Z',
      updatedAt: '2026-05-24T00:00:00.000Z'
    })

    expect(result.success).toBe(true)
  })

  it('rejects executable or unknown fields', () => {
    const result = ChoiceNodeSchema.safeParse({
      id: 'n002',
      type: 'choice',
      promptKey: 'choice.ep001.n002.prompt',
      script: 'lunaTrust += 1',
      choices: [
        {
          id: 'c1',
          textKey: 'choice.ep001.n002.c1',
          next: 'n003'
        }
      ]
    })

    expect(result.success).toBe(false)
  })

  it('parses the first episode node shape', () => {
    const result = EpisodeDefinitionSchema.safeParse({
      episodeId: 'ep_001',
      titleKey: 'ep_001.title',
      summaryKey: 'ep_001.summary',
      targetExpressionIds: ['expr_you_look_tired_today'],
      cliffhangerKey: 'ep_001.cliffhanger',
      scenes: [
        {
          id: 'scene_001',
          background: 'bg_cafe_rainy_night',
          bgm: 'bgm_soft_rain',
          startNodeId: 'n001',
          nodes: [
            {
              id: 'n001',
              type: 'dialogue',
              speaker: 'luna',
              textKey: 'line.ep001.n001',
              emotion: 'neutral',
              sprite: 'luna.neutral',
              voice: 'voices/en-US/luna/ep_001_n001.mp3',
              learningPointIds: ['expr_you_look_tired_today'],
              next: 'n002'
            },
            {
              id: 'n002',
              type: 'choice',
              promptKey: 'choice.ep001.n002.prompt',
              choices: [
                {
                  id: 'c1',
                  textKey: 'choice.ep001.n002.c1',
                  next: 'n003',
                  effects: {
                    lunaTrust: 1
                  }
                }
              ]
            }
          ]
        }
      ]
    })

    expect(result.success).toBe(true)
  })

  it('requires a story entry episode', () => {
    const result = StoryDefinitionSchema.safeParse({
      storyId: 'midnight-cafe',
      startEpisodeId: '',
      episodes: []
    })

    expect(result.success).toBe(false)
  })

  it('parses expression-level learning items', () => {
    const result = LearningItemSchema.safeParse({
      id: 'expr_i_had_a_long_day',
      type: 'expression',
      targetLanguage: 'en-US',
      text: 'I had a long day.',
      translation: '我今天过得很累。',
      level: 'A2',
      usage: 'Used when your day was tiring or stressful.',
      tokens: [
        {
          text: 'had',
          lemma: 'have',
          meaning: '经历了'
        }
      ],
      examples: [
        {
          text: 'I had a long day at work.',
          translation: '我今天上班很累。'
        }
      ]
    })

    expect(result.success).toBe(true)
  })
})
